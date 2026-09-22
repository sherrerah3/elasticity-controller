package awsx

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/aws/smithy-go"

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
}

// ejecuta las decisiones sobre EC2 + ALB. Implementa controller.Actuator.
// Todas las operaciones son NO bloqueantes: disparan la llamada a AWS y
// retornan de inmediato. El loop, tick a tick, observa el progreso real.
type Actuator struct {
	EC2      ec2API
	ELB      elbTargetsAPI
	Cfg      ActuatorConfig
	ProvSink controller.ProvisioningSink // opcional; recibe la medicion t0/t1/t2

	mu      sync.Mutex
	pending map[string]*provisioning // instancias lanzadas aun no healthy
}

// estado de una instancia en aprovisionamiento (para medir t0/t1/t2 sin bloquear).
type provisioning struct {
	t0, t1     time.Time
	registered bool
}

func NewActuator(ec2c ec2API, elb elbTargetsAPI, cfg ActuatorConfig) *Actuator {
	return &Actuator{EC2: ec2c, ELB: elb, Cfg: cfg, pending: map[string]*provisioning{}}
}

// numero de instancias gestionadas en estado pending/running (fuente de verdad).
func (a *Actuator) CurrentCapacity(ctx context.Context) (int, error) {
	ids, err := a.managedInstanceIDs(ctx)
	if err != nil {
		return 0, err
	}
	return len(ids), nil
}

// lanza una instancia y retorna de inmediato (no espera running ni healthy).
// Devuelve el ID para que el loop rastree su aprovisionamiento en ticks siguientes.
func (a *Actuator) Launch(ctx context.Context) (string, error) {
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
		Monitoring:         &ec2types.RunInstancesMonitoringEnabled{Enabled: aws.Bool(true)},
	})
	if err != nil {
		return "", fmt.Errorf("RunInstances: %w", err)
	}
	if len(out.Instances) == 0 || out.Instances[0].InstanceId == nil {
		return "", fmt.Errorf("RunInstances no devolvio InstanceId")
	}
	id := *out.Instances[0].InstanceId

	// registra la instancia para rastrear su aprovisionamiento (t0 = ahora).
	a.mu.Lock()
	a.pending[id] = &provisioning{t0: time.Now()}
	a.mu.Unlock()

	return id, nil
}

// AdvanceProvisioning avanza el aprovisionamiento de las instancias lanzadas,
// sin bloquear: por cada una pendiente, si ya esta running la registra (t1),
// y si el ALB la reporta healthy emite la medicion t0/t1/t2 (t2). Lo llama el
// loop una vez por tick.
func (a *Actuator) AdvanceProvisioning(ctx context.Context) {
	a.mu.Lock()
	ids := make([]string, 0, len(a.pending))
	for id := range a.pending {
		ids = append(ids, id)
	}
	a.mu.Unlock()

	for _, id := range ids {
		a.advanceOne(ctx, id)
	}
}

func (a *Actuator) advanceOne(ctx context.Context, id string) {
	a.mu.Lock()
	p := a.pending[id]
	a.mu.Unlock()
	if p == nil {
		return
	}

	// paso 1: esperar running para poder registrar (target_type=instance).
	if p.t1.IsZero() {
		running, err := a.isRunning(ctx, id)
		if err != nil || !running {
			return // aun no; se reintenta el proximo tick.
		}
		if err := a.register(ctx, id); err != nil {
			return // se reintenta el proximo tick.
		}
		a.mu.Lock()
		p.t1 = time.Now()
		p.registered = true
		a.mu.Unlock()
		return
	}

	// paso 2: esperar healthy para cerrar la medicion.
	state, err := a.targetState(ctx, id)
	if err != nil {
		return
	}
	if state == string(elbtypes.TargetHealthStateEnumHealthy) {
		t2 := time.Now()
		a.emitProvisioning(id, p.t0, p.t1, t2)
		a.mu.Lock()
		delete(a.pending, id)
		a.mu.Unlock()
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

// ScaleUp lanza una instancia (no bloqueante). Implementa controller.Actuator.
func (a *Actuator) ScaleUp(ctx context.Context) error {
	_, err := a.Launch(ctx)
	return err
}

// ScaleDown desregistra y termina una instancia gestionada (no bloqueante).
// El drenado de conexiones lo maneja el deregistration_delay del target group.
func (a *Actuator) ScaleDown(ctx context.Context) error {
	ids, err := a.managedInstanceIDs(ctx)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return fmt.Errorf("no hay instancias gestionadas para reducir")
	}
	victim := ids[len(ids)-1] // la mas reciente; criterio simple y suficiente para 1-5
	return a.retire(ctx, victim)
}

// Replace lanza una sustituta y retira la mala (no bloqueante). Implementa Replacer.
func (a *Actuator) Replace(ctx context.Context, badInstanceID string) error {
	if _, err := a.Launch(ctx); err != nil {
		return fmt.Errorf("reemplazo: no se pudo lanzar sustituta: %w", err)
	}
	if err := a.retire(ctx, badInstanceID); err != nil {
		return fmt.Errorf("reemplazo: no se pudo retirar %s: %w", badInstanceID, err)
	}
	return nil
}

// registra una instancia en el target group. Tolera el NotFound transitorio.
func (a *Actuator) register(ctx context.Context, id string) error {
	_, err := a.ELB.RegisterTargets(ctx, &elasticloadbalancingv2.RegisterTargetsInput{
		TargetGroupArn: aws.String(a.Cfg.TargetGroupARN),
		Targets:        []elbtypes.TargetDescription{{Id: aws.String(id), Port: aws.Int32(a.Cfg.AppPort)}},
	})
	if err != nil && !isTransientNotFound(err) {
		return fmt.Errorf("RegisterTargets(%s): %w", id, err)
	}
	return nil
}

// indica si una instancia esta en estado running.
func (a *Actuator) isRunning(ctx context.Context, id string) (bool, error) {
	out, err := a.EC2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{id}})
	if err != nil {
		if isTransientNotFound(err) {
			return false, nil // aun no se propaga: no es running todavia.
		}
		return false, fmt.Errorf("DescribeInstances: %w", err)
	}
	return instanceState(out, id) == "running", nil
}

// estado del target en el ALB ("" si no aparece).
func (a *Actuator) targetState(ctx context.Context, id string) (string, error) {
	out, err := a.ELB.DescribeTargetHealth(ctx, &elasticloadbalancingv2.DescribeTargetHealthInput{
		TargetGroupArn: aws.String(a.Cfg.TargetGroupARN),
		Targets:        []elbtypes.TargetDescription{{Id: aws.String(id), Port: aws.Int32(a.Cfg.AppPort)}},
	})
	if err != nil {
		if isTransientNotFound(err) {
			return "", nil
		}
		return "", fmt.Errorf("DescribeTargetHealth(%s): %w", id, err)
	}
	for _, d := range out.TargetHealthDescriptions {
		if d.TargetHealth != nil {
			return string(d.TargetHealth.State), nil
		}
	}
	return "", nil
}

// retire desregistra del target group y termina la instancia (no bloqueante).
func (a *Actuator) retire(ctx context.Context, id string) error {
	if _, err := a.ELB.DeregisterTargets(ctx, &elasticloadbalancingv2.DeregisterTargetsInput{
		TargetGroupArn: aws.String(a.Cfg.TargetGroupARN),
		Targets:        []elbtypes.TargetDescription{{Id: aws.String(id), Port: aws.Int32(a.Cfg.AppPort)}},
	}); err != nil && !isTransientNotFound(err) {
		return fmt.Errorf("DeregisterTargets(%s): %w", id, err)
	}
	if _, err := a.EC2.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{id}}); err != nil {
		return fmt.Errorf("TerminateInstances(%s): %w", id, err)
	}
	return nil
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

// devuelve nil si la cadena esta vacia (para campos opcionales del SDK).
func optString(s string) *string {
	if s == "" {
		return nil
	}
	return aws.String(s)
}

// true si el error es un "aun no existe" transitorio por consistencia eventual
// de AWS (el ID recien creado todavia no se propaga). No es un fallo real.
func isTransientNotFound(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		return code == "InvalidInstanceID.NotFound" || code == "InvalidTarget.NotFound"
	}
	return false
}
