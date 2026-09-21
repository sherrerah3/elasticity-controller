package main

import (
	"context"
	"encoding/base64"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"autoscaler/awsx"
	"autoscaler/controller"
)

func main() {
	// operacion
	interval := flag.Duration("interval", 60*time.Second, "intervalo entre ciclos de evaluacion")
	logPath := flag.String("log", "decisions.jsonl", "ruta del log de decisiones (JSON Lines)")
	provPath := flag.String("prov-log", "provisioning.jsonl", "ruta del log de aprovisionamiento t0/t1/t2 (JSON Lines)")
	region := flag.String("region", "us-east-1", "region de AWS")
	maxRetries := flag.Int("max-retries", 3, "reintentos del SDK ante errores reintentables")
	dryRun := flag.Bool("dry-run", false, "usa stubs en memoria en vez de AWS (para pruebas locales)")

	// recursos (vienen de los outputs de Terraform)
	tgARN := flag.String("target-group-arn", "", "ARN del target group")
	tgDim := flag.String("tg-dimension", "", "dimension TargetGroup de CloudWatch (targetgroup/.../id)")
	lbDim := flag.String("lb-dimension", "", "dimension LoadBalancer de CloudWatch (app/.../id)")
	subnetID := flag.String("subnet-id", "", "subred privada donde lanzar instancias")
	sgID := flag.String("app-sg-id", "", "security group de las instancias de la app")
	ami := flag.String("ami", "", "AMI de la app")
	instanceType := flag.String("instance-type", "t3.micro", "tipo de instancia de la app")
	iamProfile := flag.String("iam-profile", "", "instance profile (opcional)")
	keyName := flag.String("key-name", "", "key pair EC2 (opcional)")
	userDataFile := flag.String("user-data-file", "", "archivo con el user-data de la app (opcional)")
	appPort := flag.Int("app-port", 8080, "puerto de la app")
	managedTagKey := flag.String("managed-tag-key", "autoscaler-managed", "clave del tag de instancias gestionadas")
	managedTagValue := flag.String("managed-tag-value", "true", "valor del tag de instancias gestionadas")
	deregDelay := flag.Duration("deregister-delay", 90*time.Second, "maximo a esperar el drenado antes de terminar")
	runningTimeout := flag.Duration("running-timeout", 3*time.Minute, "maximo a esperar el estado running (t1)")
	healthyTimeout := flag.Duration("healthy-timeout", 5*time.Minute, "maximo a esperar el estado healthy (t2)")
	pollInterval := flag.Duration("poll-interval", 5*time.Second, "cada cuanto sondear estado durante el aprovisionamiento")
	flag.Parse()

	cfg := controller.DefaultPolicyConfig()

	// sink de aprovisionamiento (solo con AWS real); se inyecta al actuador.
	var provSink controller.ProvisioningSink
	if !*dryRun {
		sink, provFile, err := controller.OpenProvisioningFile(*provPath)
		if err != nil {
			log.Fatalf("no se pudo abrir el log de aprovisionamiento %q: %v", *provPath, err)
		}
		defer provFile.Close()
		provSink = sink
	}

	obs, act := buildComponents(context.Background(), buildArgs{
		dryRun: *dryRun, region: *region, maxRetries: *maxRetries,
		minInstances: cfg.MinInstances, window: *interval,
		tgARN: *tgARN, tgDim: *tgDim, lbDim: *lbDim,
		subnetID: *subnetID, sgID: *sgID, ami: *ami, instanceType: *instanceType,
		iamProfile: *iamProfile, keyName: *keyName, userDataFile: *userDataFile,
		appPort: int32(*appPort), managedTagKey: *managedTagKey, managedTagValue: *managedTagValue,
		deregDelay: *deregDelay, runningTimeout: *runningTimeout, healthyTimeout: *healthyTimeout,
		pollInterval: *pollInterval, provSink: provSink,
	})

	loop := controller.NewLoop(obs, act, cfg, *interval)

	logger, logFile, err := controller.OpenDecisionLogFile(*logPath)
	if err != nil {
		log.Fatalf("no se pudo abrir el log de decisiones %q: %v", *logPath, err)
	}
	defer logFile.Close()
	loop.Logger = logger

	// SIGINT/SIGTERM cancelan el contexto para un apagado limpio.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("controller iniciado (intervalo=%s, dry-run=%v)", *interval, *dryRun)
	loop.Run(ctx)
	log.Println("controller detenido")
}

type buildArgs struct {
	dryRun                         bool
	region                         string
	maxRetries                     int
	minInstances                   int
	window                         time.Duration
	tgARN, tgDim, lbDim            string
	subnetID, sgID, ami            string
	instanceType, iamProfile       string
	keyName, userDataFile          string
	appPort                        int32
	managedTagKey, managedTagValue string
	deregDelay                     time.Duration
	runningTimeout, healthyTimeout time.Duration
	pollInterval                   time.Duration
	provSink                       controller.ProvisioningSink
}

// construye observador y actuador reales (o stubs si dry-run).
func buildComponents(ctx context.Context, a buildArgs) (controller.MetricsObserver, controller.Actuator) {
	if a.dryRun {
		return &stubObserver{}, &stubActuator{capacity: a.minInstances}
	}

	clients, err := awsx.NewClients(ctx, a.region, a.maxRetries)
	if err != nil {
		log.Fatalf("no se pudieron crear los clientes de AWS: %v", err)
	}

	obs := &awsx.Observer{
		CW: clients.CW, ELB: clients.ELB,
		TargetGroupARN: a.tgARN, TGDimension: a.tgDim, LBDimension: a.lbDim,
		Window: a.window,
	}

	act := awsx.NewActuator(clients.EC2, clients.ELB, awsx.ActuatorConfig{
		AMI: a.ami, InstanceType: a.instanceType, SubnetID: a.subnetID, SecurityGroupID: a.sgID,
		IAMInstanceProfile: a.iamProfile, KeyName: a.keyName,
		UserDataBase64: loadUserData(a.userDataFile),
		ManagedTagKey:  a.managedTagKey, ManagedTagValue: a.managedTagValue,
		TargetGroupARN: a.tgARN, AppPort: a.appPort,
		RunningTimeout: a.runningTimeout, HealthyTimeout: a.healthyTimeout,
		DeregisterDelay: a.deregDelay, PollInterval: a.pollInterval,
	})
	act.ProvSink = a.provSink
	return obs, act
}

// lee el user-data de un archivo y lo codifica en base64 (vacio si no hay archivo).
func loadUserData(path string) string {
	if path == "" {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("no se pudo leer el user-data %q: %v", path, err)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// stubObserver siempre reporta datos insuficientes (para dry-run).
type stubObserver struct{}

func (o *stubObserver) Observe(ctx context.Context) (controller.Signals, error) {
	return controller.Signals{Timestamp: time.Now(), Valid: false}, nil
}

// stubActuator mantiene una capacidad en memoria (para dry-run).
type stubActuator struct{ capacity int }

func (a *stubActuator) CurrentCapacity(ctx context.Context) (int, error) { return a.capacity, nil }
func (a *stubActuator) ScaleUp(ctx context.Context) error                { a.capacity++; return nil }
func (a *stubActuator) ScaleDown(ctx context.Context) error              { a.capacity--; return nil }
