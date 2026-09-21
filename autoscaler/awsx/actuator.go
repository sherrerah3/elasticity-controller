package awsx

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"

	"autoscaler/controller"
)

// subconjunto del SDK de EC2 que usamos (mockeable en tests).
type ec2API interface {
	RunInstances(ctx context.Context, in *ec2.RunInstancesInput, optFns ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error)
	TerminateInstances(ctx context.Context, in *ec2.TerminateInstancesInput, optFns ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error)
	DescribeInstances(ctx context.Context, in *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}

// subconjunto del SDK de ELBv2 para registro/desregistro y salud de targets.
type elbTargetsAPI interface {
	RegisterTargets(ctx context.Context, in *elasticloadbalancingv2.RegisterTargetsInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.RegisterTargetsOutput, error)
	DeregisterTargets(ctx context.Context, in *elasticloadbalancingv2.DeregisterTargetsInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DeregisterTargetsOutput, error)
	DescribeTargetHealth(ctx context.Context, in *elasticloadbalancingv2.DescribeTargetHealthInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTargetHealthOutput, error)
}

// parametros para lanzar instancias de la app (vienen de Terraform/flags).
type ActuatorConfig struct {
	AMI                string
	InstanceType       string
	SubnetID           string
	SecurityGroupID    string
	IAMInstanceProfile string
	KeyName            string
	UserDataBase64     string
	ManagedTagKey      string // p.ej. "autoscaler-managed"
	ManagedTagValue    string // p.ej. "true"
	TargetGroupARN     string
	AppPort            int32

	// tiempos de espera bloqueante durante el aprovisionamiento y el retiro.
	RunningTimeout  time.Duration // maximo a esperar el estado running (t1)
	HealthyTimeout  time.Duration // maximo a esperar el estado healthy (t2)
	DeregisterDelay time.Duration // maximo a esperar el drenado antes de terminar
	PollInterval    time.Duration // cada cuanto sondear el estado
}

// ejecuta las decisiones sobre EC2 + ALB. Implementa controller.Actuator.
type Actuator struct {
	EC2      ec2API
	ELB      elbTargetsAPI
	Cfg      ActuatorConfig
	ProvSink controller.ProvisioningSink // opcional; recibe la medicion t0/t1/t2

	mu          sync.Mutex
	launchTimes map[string]time.Time // t0 por instancia (historico)
}

func NewActuator(ec2c ec2API, elb elbTargetsAPI, cfg ActuatorConfig) *Actuator {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 5 * time.Second
	}
	return &Actuator{EC2: ec2c, ELB: elb, Cfg: cfg, launchTimes: map[string]time.Time{}}
}

// numero de instancias gestionadas en estado pending/running (fuente de verdad).
func (a *Actuator) CurrentCapacity(ctx context.Context) (int, error) {
	ids, err := a.managedInstanceIDs(ctx)
	if err != nil {
		return 0, err
	}
	return len(ids), nil
}

// lanza una instancia y espera hasta que este disponible de verdad:
// RunInstances (t0) -> running (t1) -> RegisterTargets -> healthy (t2).
// Bloqueante con timeout. Emite la medicion de aprovisionamiento al sink.
func (a *Actuator) ScaleUp(ctx context.Context) error {
	out, err := a.EC2.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:            aws.String(a.Cfg.AMI),
		InstanceType:       ec2types.InstanceType(a.Cfg.InstanceType),
		MinCount:           aws.Int32(1),
		MaxCount:           aws.Int32(1),
		SubnetId:           aws.String(a.Cfg.SubnetID),
		SecurityGroupIds:   []string{a.Cfg.SecurityGroupID},
		KeyName:            optString(a.Cfg.KeyName),
		UserData:           optString(a.Cfg.UserDataBase64),
		IamInstanceProfile: a.iamProfile(),
		TagSpecifications:  a.tagSpec(),
	})
	if err != nil {
		return fmt.Errorf("RunInstances: %w", err)
	}
	if len(out.Instances) == 0 || out.Instances[0].InstanceId == nil {
		return fmt.Errorf("RunInstances no devolvio InstanceId")
	}
	id := *out.Instances[0].InstanceId

	t0 := time.Now()
	a.mu.Lock()
	a.launchTimes[id] = t0
	a.mu.Unlock()

	// t1: para target_type=instance, AWS exige running antes de registrar.
	t1, err := a.waitRunning(ctx, id)
	if err != nil {
		return fmt.Errorf("esperando running(%s): %w", id, err)
	}

	if _, err := a.ELB.RegisterTargets(ctx, &elasticloadbalancingv2.RegisterTargetsInput{
		TargetGroupArn: aws.String(a.Cfg.TargetGroupARN),
		Targets:        []elbtypes.TargetDescription{{Id: aws.String(id), Port: aws.Int32(a.Cfg.AppPort)}},
	}); err != nil {
		return fmt.Errorf("RegisterTargets(%s): %w", id, err)
	}

	// t2: el momento real en que empieza a recibir trafico.
	t2, err := a.waitHealthy(ctx, id)
	if err != nil {
		return fmt.Errorf("esperando healthy(%s): %w", id, err)
	}

	a.emitProvisioning(id, t0, t1, t2)
	return nil
}

// desregistra una instancia, espera el drenado y la termina.
func (a *Actuator) ScaleDown(ctx context.Context) error {
	ids, err := a.managedInstanceIDs(ctx)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return fmt.Errorf("no hay instancias gestionadas para reducir")
	}
	victim := ids[len(ids)-1] // la mas reciente; criterio simple y suficiente para 1-5
	return a.deregisterAndTerminate(ctx, victim)
}

// reemplaza una instancia unhealthy: lanza una nueva y luego termina la mala.
// Orden lanzar->terminar para no bajar de capacidad (implementa controller.Replacer).
func (a *Actuator) Replace(ctx context.Context, badInstanceID string) error {
	if err := a.ScaleUp(ctx); err != nil {
		return fmt.Errorf("reemplazo: no se pudo lanzar sustituta: %w", err)
	}
	if err := a.deregisterAndTerminate(ctx, badInstanceID); err != nil {
		return fmt.Errorf("reemplazo: no se pudo retirar %s: %w", badInstanceID, err)
	}
	return nil
}

// espera hasta que la instancia este running (t1), sondeando cada PollInterval.
func (a *Actuator) waitRunning(ctx context.Context, id string) (time.Time, error) {
	deadline := time.Now().Add(a.Cfg.RunningTimeout)
	for {
		out, err := a.EC2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{id}})
		if err != nil {
			return time.Time{}, fmt.Errorf("DescribeInstances: %w", err)
		}
		if instanceState(out, id) == "running" {
			return time.Now(), nil
		}
		if a.Cfg.RunningTimeout > 0 && time.Now().After(deadline) {
			return time.Time{}, fmt.Errorf("timeout esperando running")
		}
		if err := sleepCtx(ctx, a.Cfg.PollInterval); err != nil {
			return time.Time{}, err
		}
	}
}

// espera hasta que el target este healthy en el ALB (t2), sondeando cada PollInterval.
func (a *Actuator) waitHealthy(ctx context.Context, id string) (time.Time, error) {
	deadline := time.Now().Add(a.Cfg.HealthyTimeout)
	for {
		state, err := a.targetState(ctx, id)
		if err != nil {
			return time.Time{}, err
		}
		if state == string(elbtypes.TargetHealthStateEnumHealthy) {
			return time.Now(), nil
		}
		if a.Cfg.HealthyTimeout > 0 && time.Now().After(deadline) {
			return time.Time{}, fmt.Errorf("timeout esperando healthy (ultimo estado: %s)", state)
		}
		if err := sleepCtx(ctx, a.Cfg.PollInterval); err != nil {
			return time.Time{}, err
		}
	}
}

// desregistra del target group, espera el drenado y termina la instancia dada.
func (a *Actuator) deregisterAndTerminate(ctx context.Context, id string) error {
	if _, err := a.ELB.DeregisterTargets(ctx, &elasticloadbalancingv2.DeregisterTargetsInput{
		TargetGroupArn: aws.String(a.Cfg.TargetGroupARN),
		Targets:        []elbtypes.TargetDescription{{Id: aws.String(id), Port: aws.Int32(a.Cfg.AppPort)}},
	}); err != nil {
		return fmt.Errorf("DeregisterTargets(%s): %w", id, err)
	}

	// espera a que el target termine de drenar (deja de estar draining) antes de
	// terminar, en vez de un sleep fijo. DeregisterDelay es el timeout maximo.
	if err := a.waitDrained(ctx, id); err != nil {
		return err
	}

	if _, err := a.EC2.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{id}}); err != nil {
		return fmt.Errorf("TerminateInstances(%s): %w", id, err)
	}
	a.mu.Lock()
	delete(a.launchTimes, id)
	a.mu.Unlock()
	return nil
}

// espera a que el target deje de estar 'draining'. Termina cuando llega a
// 'unused' o desaparece del target group, o al agotar DeregisterDelay (timeout).
func (a *Actuator) waitDrained(ctx context.Context, id string) error {
	if a.Cfg.DeregisterDelay <= 0 {
		return nil // sin espera configurada: terminar de una vez.
	}
	deadline := time.Now().Add(a.Cfg.DeregisterDelay)
	for {
		state, err := a.targetState(ctx, id)
		if err != nil {
			return err
		}
		// "" (ya no aparece) o "unused": el drenado termino.
		if state == "" || state == string(elbtypes.TargetHealthStateEnumUnused) {
			return nil
		}
		if time.Now().After(deadline) {
			return nil // timeout: el drenado tardo demasiado, terminamos igual.
		}
		if err := sleepCtx(ctx, a.Cfg.PollInterval); err != nil {
			return err
		}
	}
}

// escribe la medicion de aprovisionamiento al sink, si hay uno.
func (a *Actuator) emitProvisioning(id string, t0, t1, t2 time.Time) {
	if a.ProvSink == nil {
		return
	}
	_ = a.ProvSink.Record(controller.ProvisioningRecord{
		InstanceID:    id,
		T0:            t0,
		T1:            t1,
		T2:            t2,
		AWSOverhead:   t1.Sub(t0).Seconds(),
		AppStartup:    t2.Sub(t1).Seconds(),
		TotalProvSecs: t2.Sub(t0).Seconds(),
	})
}

// estado de salud de un target concreto en el target group ("" si no aparece).
func (a *Actuator) targetState(ctx context.Context, id string) (string, error) {
	out, err := a.ELB.DescribeTargetHealth(ctx, &elasticloadbalancingv2.DescribeTargetHealthInput{
		TargetGroupArn: aws.String(a.Cfg.TargetGroupARN),
		Targets:        []elbtypes.TargetDescription{{Id: aws.String(id), Port: aws.Int32(a.Cfg.AppPort)}},
	})
	if err != nil {
		return "", fmt.Errorf("DescribeTargetHealth(%s): %w", id, err)
	}
	for _, d := range out.TargetHealthDescriptions {
		if d.TargetHealth != nil {
			return string(d.TargetHealth.State), nil
		}
	}
	return "", nil
}

// instancias con el tag gestionado y en estado pending/running.
func (a *Actuator) managedInstanceIDs(ctx context.Context) ([]string, error) {
	out, err := a.EC2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("tag:" + a.Cfg.ManagedTagKey), Values: []string{a.Cfg.ManagedTagValue}},
			{Name: aws.String("instance-state-name"), Values: []string{"pending", "running"}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("DescribeInstances: %w", err)
	}
	var ids []string
	for _, r := range out.Reservations {
		for _, inst := range r.Instances {
			if inst.InstanceId != nil {
				ids = append(ids, *inst.InstanceId)
			}
		}
	}
	return ids, nil
}

func (a *Actuator) tagSpec() []ec2types.TagSpecification {
	return []ec2types.TagSpecification{{
		ResourceType: ec2types.ResourceTypeInstance,
		Tags: []ec2types.Tag{
			{Key: aws.String(a.Cfg.ManagedTagKey), Value: aws.String(a.Cfg.ManagedTagValue)},
			{Key: aws.String("Name"), Value: aws.String("autoscaler-app")},
		},
	}}
}

func (a *Actuator) iamProfile() *ec2types.IamInstanceProfileSpecification {
	if a.Cfg.IAMInstanceProfile == "" {
		return nil
	}
	return &ec2types.IamInstanceProfileSpecification{Name: aws.String(a.Cfg.IAMInstanceProfile)}
}

// estado ("running", "pending", ...) de una instancia en la salida de DescribeInstances.
func instanceState(out *ec2.DescribeInstancesOutput, id string) string {
	for _, r := range out.Reservations {
		for _, inst := range r.Instances {
			if inst.InstanceId != nil && *inst.InstanceId == id && inst.State != nil {
				return string(inst.State.Name)
			}
		}
	}
	return ""
}

// duerme d respetando la cancelacion del contexto.
func sleepCtx(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// devuelve nil si la cadena esta vacia (para campos opcionales del SDK).
func optString(s string) *string {
	if s == "" {
		return nil
	}
	return aws.String(s)
}
