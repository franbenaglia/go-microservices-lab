package platform

import (
	"crypto/rand"
	"encoding/hex"
	"os"
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
