# Despliegue

Artefactos e instrucciones para desplegar el controlador en AWS y probarlo
end-to-end.

## Contenido

- **`DESPLIEGUE.md`** — guía paso a paso completa: desde levantar la
  infraestructura hasta las pruebas de escalado y auto-sanación, y la recolección
  de evidencia. **Empieza aquí.**
- **`autoscaler-controller.service`** — unidad systemd que corre el binario del
  controlador en la instancia EC2 del controller. Los valores `<...>` se
  reemplazan con los outputs de Terraform durante el despliegue.

## Resumen del flujo

1. Cargar credenciales + `terraform apply` (levanta la infra + instancia semilla).
2. (Opcional) validar permisos con `cmd/observe`.
3. Cross-compilar el binario Go para Linux.
4. Copiarlo al controlador vía SSH saltando por el bastión (ProxyJump).
5. Instalarlo como servicio systemd y arrancarlo.
6. Probar: generar carga con k6 (escalado por demanda) y matar la app en una
   instancia (auto-sanación).
7. Bajar los logs de evidencia y `terraform destroy`.

## Por qué SSH con salto (ProxyJump)

El controlador vive en una subred **privada** (sin IP pública, por seguridad).
La única forma de llegar a él es saltando por el **bastión**, que sí está en una
subred pública. Por eso el `scp` y el `ssh` usan `ProxyJump`.

Ver el detalle completo en `DESPLIEGUE.md`.
