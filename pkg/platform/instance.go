package platform

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
)

// InstanceID devuelve un identificador único de la instancia. Se usa el valor de
// INSTANCE_ID si existe; si no, el hostname (en Docker, el ID corto del
// contenedor) más un sufijo aleatorio para distinguir reinicios.
func InstanceID(service string) string {
	if v := os.Getenv("INSTANCE_ID"); v != "" {
		return v
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = service
	}
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return host + "-" + hex.EncodeToString(b)
}

// Hostname devuelve el hostname o una cadena vacía.
func Hostname() string {
	h, _ := os.Hostname()
	return h
}

// InstanceWeight lee INSTANCE_WEIGHT (1..1000). Se publica como metadato
// "weight" en el registro, y el selector wrr de Kratos lo usa como peso del
// nodo (100 por defecto). Devuelve "" si no está definido.
func InstanceWeight() (string, error) {
	v := os.Getenv("INSTANCE_WEIGHT")
	if v == "" {
		return "", nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 1000 {
		return "", fmt.Errorf("INSTANCE_WEIGHT inválido %q: entero entre 1 y 1000", v)
	}
	return strconv.Itoa(n), nil
}
