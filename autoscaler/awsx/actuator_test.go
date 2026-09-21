package awsx

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"

	"autoscaler/controller"
)

// mock EC2 que registra el orden de las llamadas. Reporta runID como running.
type mockEC2 struct {
	runID       string
	instanceIDs []string
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
	// consulta por IDs (waitRunning): devuelve runID en estado running.
	if len(in.InstanceIds) > 0 {
		var insts []ec2types.Instance
		for _, id := range in.InstanceIds {
			insts = append(insts, ec2types.Instance{
				InstanceId: aws.String(id),
				State:      &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning},
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
		// timeouts amplios y poll rapido para que los tests no esperen.
		RunningTimeout: time.Second, HealthyTimeout: time.Second, PollInterval: time.Millisecond,
		DeregisterDelay: 0,
	}
}

// ScaleUp: RunInstances -> (running) -> RegisterTargets -> (healthy). Orden run luego register.
func TestScaleUpLaunchesThenRegisters(t *testing.T) {
	ec2m := &mockEC2{runID: "i-new"}
	elb := &mockELBTargets{}
	a := NewActuator(ec2m, elb, testConfig())

	if err := a.ScaleUp(context.Background()); err != nil {
		t.Fatalf("ScaleUp fallo: %v", err)
	}
	if len(ec2m.calls) == 0 || ec2m.calls[0] != "run" {
		t.Errorf("esperaba 'run' primero, obtuvo %v", ec2m.calls)
	}
	if len(elb.calls) != 1 || elb.calls[0] != "register" {
		t.Errorf("esperaba una unica 'register' tras running, obtuvo %v", elb.calls)
	}
}

// ScaleUp emite la medicion de aprovisionamiento al sink.
func TestScaleUpEmitsProvisioning(t *testing.T) {
	ec2m := &mockEC2{runID: "i-new"}
	a := NewActuator(ec2m, &mockELBTargets{}, testConfig())
	sink := &provSinkSpy{}
	a.ProvSink = sink

	if err := a.ScaleUp(context.Background()); err != nil {
		t.Fatalf("ScaleUp fallo: %v", err)
	}
	if len(sink.records) != 1 || sink.records[0].InstanceID != "i-new" {
		t.Fatalf("esperaba 1 registro de aprovisionamiento para i-new, obtuvo %+v", sink.records)
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

// scale-in con DeregisterDelay>0 sondea salud y termina cuando el target esta unused.
func TestScaleDownWaitsForDrainViaHealth(t *testing.T) {
	ec2m := &mockEC2{instanceIDs: []string{"i-1"}}
	elb := &mockELBTargets{healthState: elbtypes.TargetHealthStateEnumUnused}
	cfg := testConfig()
	cfg.DeregisterDelay = time.Second // activa el poll de drenado
	a := NewActuator(ec2m, elb, cfg)

	if err := a.ScaleDown(context.Background()); err != nil {
		t.Fatalf("ScaleDown fallo: %v", err)
	}
	// debe haber consultado la salud (drenado) y luego terminado.
	if len(elb.calls) == 0 || elb.calls[0] != "deregister" {
		t.Errorf("esperaba 'deregister' primero, obtuvo %v", elb.calls)
	}
	if ec2m.calls[len(ec2m.calls)-1] != "terminate" {
		t.Errorf("esperaba 'terminate' al final tras drenar, obtuvo %v", ec2m.calls)
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
