// comando de solo-lectura: observa una vez y valida permisos de AWS. No actua.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"time"

	"autoscaler/awsx"
)

func main() {
	region := flag.String("region", "us-east-1", "region de AWS")
	maxRetries := flag.Int("max-retries", 3, "reintentos del SDK")
	tgARN := flag.String("target-group-arn", "", "ARN del target group")
	tgDim := flag.String("tg-dimension", "", "dimension TargetGroup de CloudWatch (targetgroup/.../id)")
	lbDim := flag.String("lb-dimension", "", "dimension LoadBalancer de CloudWatch (app/.../id)")
	lookback := flag.Duration("lookback", 5*time.Minute, "cuanto mirar hacia atras (amplio, por el retraso de CloudWatch)")
	flag.Parse()

	if *tgARN == "" {
		log.Fatal("falta -target-group-arn (obtenlo de: terraform output target_group_arn)")
	}

	ctx := context.Background()
	clients, err := awsx.NewClients(ctx, *region, *maxRetries)
	if err != nil {
		log.Fatalf("no se pudieron crear los clientes de AWS: %v", err)
	}

	obs := &awsx.Observer{
		CW:             clients.CW,
		ELB:            clients.ELB,
		TargetGroupARN: *tgARN,
		TGDimension:    *tgDim,
		LBDimension:    *lbDim,
		Lookback:       *lookback,
	}

	fmt.Println("Observando una vez (solo lectura, sin actuar)...")
	sig, err := obs.Observe(ctx)
	if err != nil {
		log.Fatalf("Observe fallo (revisa permisos o los identificadores): %v", err)
	}

	pretty, _ := json.MarshalIndent(sig, "", "  ")
	fmt.Println(string(pretty))

	if sig.Valid {
		fmt.Println("\nOK: señal valida. La observacion funciona y hay instancias sanas.")
	} else {
		fmt.Println("\nSeñal invalida (Valid=false). Coherente si aun no hay instancias sanas;")
		fmt.Println("las llamadas a AWS no fallaron por permisos, que es lo que queriamos confirmar.")
	}
}
