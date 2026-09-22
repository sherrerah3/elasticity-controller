package awsx

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"

	"autoscaler/controller"
)

// mock EC2 que registra las llamadas. running controla el estado reportado.
type mockEC2 struct {
	runID       string
	instanceIDs []string
	running     bool // si true, DescribeInstances por ID reporta "running"
	calls       []string
}

func (m *mockEC2) RunInstances(ctx context.Context, in *ec2.RunInstancesInput, optFns ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	m.calls = append(m.calls, "run")
	return &ec2.RunInstancesOutput{
		Instances: []ec2types.Instance{{InstanceId: aws.String(m.runID)}},
	}, nil
}
func (m *mockEC2) TerminateInstances(ctx context.Context, in *ec2.TerminateInstancesInput, optFns ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
	m.calls = append(m.calls, "terminate")
	return &ec2.TerminateInstancesOutput{}, nil
}
func (m *mockEC2) DescribeInstances(ctx context.Context, in *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	// consulta por IDs: reporta el estado segun m.running.
	if len(in.InstanceIds) > 0 {
		state := ec2types.InstanceStateNamePending
		if m.running {
			state = ec2types.InstanceStateNameRunning
		}
		var insts []ec2types.Instance
		for _, id := range in.InstanceIds {
			insts = append(insts, ec2types.Instance{
				InstanceId: aws.String(id),
				State:      &ec2types.InstanceState{Name: state},
			})
		}
		return &ec2.DescribeInstancesOutput{Reservations: []ec2types.Reservation{{Instances: insts}}}, nil
	}
	// consulta por filtros (managedInstanceIDs): devuelve la lista configurada.
	var insts []ec2types.Instance
	for _, id := range m.instanceIDs {
		insts = append(insts, ec2types.Instance{InstanceId: aws.String(id)})
	}
	return &ec2.DescribeInstancesOutput{Reservations: []ec2types.Reservation{{Instances: insts}}}, nil
}

// mock ELB: registra llamadas y reporta el target con un estado configurable.
type mockELBTargets struct {
	calls       []string
	healthState elbtypes.TargetHealthStateEnum // estado a devolver (default healthy)
}

func (m *mockELBTargets) RegisterTargets(ctx context.Context, in *elasticloadbalancingv2.RegisterTargetsInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.RegisterTargetsOutput, error) {
	m.calls = append(m.calls, "register")
	return &elasticloadbalancingv2.RegisterTargetsOutput{}, nil
}
func (m *mockELBTargets) DeregisterTargets(ctx context.Context, in *elasticloadbalancingv2.DeregisterTargetsInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DeregisterTargetsOutput, error) {
	m.calls = append(m.calls, "deregister")
	return &elasticloadbalancingv2.DeregisterTargetsOutput{}, nil
}
func (m *mockELBTargets) DescribeTargetHealth(ctx context.Context, in *elasticloadbalancingv2.DescribeTargetHealthInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTargetHealthOutput, error) {
	state := m.healthState
	if state == "" {
		state = elbtypes.TargetHealthStateEnumHealthy
	}
	return &elasticloadbalancingv2.DescribeTargetHealthOutput{
		TargetHealthDescriptions: []elbtypes.TargetHealthDescription{
			{TargetHealth: &elbtypes.TargetHealth{State: state}},
		},
	}, nil
}

func testConfig() ActuatorConfig {
	return ActuatorConfig{
		AMI: "ami-x", InstanceType: "t3.micro", SubnetID: "subnet-x", SecurityGroupID: "sg-x",
		ManagedTagKey: "autoscaler-managed", ManagedTagValue: "true",
		TargetGroupARN: "arn:tg", AppPort: 8080,
	}
}

// ScaleUp solo lanza (no bloqueante): dispara RunInstances y NO registra todavia.
func TestScaleUpOnlyLaunches(t *testing.T) {
	ec2m := &mockEC2{runID: "i-new"}
	elb := &mockELBTargets{}
	a := NewActuator(ec2m, elb, testConfig())

	if err := a.ScaleUp(context.Background()); err != nil {
		t.Fatalf("ScaleUp fallo: %v", err)
	}
	if len(ec2m.calls) != 1 || ec2m.calls[0] != "run" {
		t.Errorf("esperaba solo 'run', obtuvo %v", ec2m.calls)
	}
	if len(elb.calls) != 0 {
		t.Errorf("ScaleUp no debe registrar aun (eso lo hace AdvanceProvisioning), obtuvo %v", elb.calls)
	}
}

// AdvanceProvisioning: cuando la instancia esta running la registra (t1) y
// cuando esta healthy emite la medicion t0/t1/t2.
func TestAdvanceProvisioningRegistersThenMeasures(t *testing.T) {
	ec2m := &mockEC2{runID: "i-new", running: false}
	elb := &mockELBTargets{}
	a := NewActuator(ec2m, elb, testConfig())
	sink := &provSinkSpy{}
	a.ProvSink = sink

	// lanzar (queda pendiente, aun no running).
	if err := a.ScaleUp(context.Background()); err != nil {
		t.Fatalf("ScaleUp fallo: %v", err)
	}

	// tick 1: aun pending -> no registra ni emite.
	a.AdvanceProvisioning(context.Background())
	if len(elb.calls) != 0 {
		t.Errorf("no debia registrar mientras pending, obtuvo %v", elb.calls)
	}

	// ahora pasa a running -> siguiente advance registra (t1).
	ec2m.running = true
	a.AdvanceProvisioning(context.Background())
	if len(elb.calls) == 0 || elb.calls[0] != "register" {
		t.Errorf("esperaba 'register' al estar running, obtuvo %v", elb.calls)
	}
	if len(sink.records) != 0 {
		t.Errorf("no debia emitir aun (falta healthy), obtuvo %d", len(sink.records))
	}

	// el mock reporta healthy por defecto -> siguiente advance emite (t2).
	a.AdvanceProvisioning(context.Background())
	if len(sink.records) != 1 || sink.records[0].InstanceID != "i-new" {
		t.Fatalf("esperaba 1 medicion para i-new, obtuvo %+v", sink.records)
	}
}

// ScaleDown desregistra ANTES de terminar (orden correcto).
func TestScaleDownDeregistersBeforeTerminate(t *testing.T) {
	ec2m := &mockEC2{instanceIDs: []string{"i-1", "i-2"}}
	elb := &mockELBTargets{}
	a := NewActuator(ec2m, elb, testConfig())

	if err := a.ScaleDown(context.Background()); err != nil {
		t.Fatalf("ScaleDown fallo: %v", err)
	}
	if len(elb.calls) != 1 || elb.calls[0] != "deregister" {
		t.Errorf("esperaba 'deregister', obtuvo %v", elb.calls)
	}
	if len(ec2m.calls) == 0 || ec2m.calls[len(ec2m.calls)-1] != "terminate" {
		t.Errorf("esperaba 'terminate' al final, obtuvo %v", ec2m.calls)
	}
}

// Replace lanza una sustituta y retira la mala.
func TestReplaceLaunchesAndRetires(t *testing.T) {
	ec2m := &mockEC2{runID: "i-new"}
	elb := &mockELBTargets{}
	a := NewActuator(ec2m, elb, testConfig())

	if err := a.Replace(context.Background(), "i-bad"); err != nil {
		t.Fatalf("Replace fallo: %v", err)
	}
	// debe haber lanzado (run) y terminado la mala (terminate).
	hasRun, hasTerminate := false, false
	for _, c := range ec2m.calls {
		if c == "run" {
			hasRun = true
		}
		if c == "terminate" {
			hasTerminate = true
		}
	}
	if !hasRun || !hasTerminate {
		t.Errorf("esperaba run + terminate, obtuvo %v", ec2m.calls)
	}
}

// CurrentCapacity cuenta las instancias gestionadas.
func TestCurrentCapacityCountsManaged(t *testing.T) {
	ec2m := &mockEC2{instanceIDs: []string{"i-1", "i-2", "i-3"}}
	a := NewActuator(ec2m, &mockELBTargets{}, testConfig())

	n, err := a.CurrentCapacity(context.Background())
	if err != nil {
		t.Fatalf("CurrentCapacity fallo: %v", err)
	}
	if n != 3 {
		t.Errorf("esperaba 3, obtuvo %d", n)
	}
}

// sink en memoria para verificar la emision de aprovisionamiento.
type provSinkSpy struct {
	records []controller.ProvisioningRecord
}

func (s *provSinkSpy) Record(r controller.ProvisioningRecord) error {
	s.records = append(s.records, r)
	return nil
}
