// Package httpcodec hace que la API REST use el mapeo JSON canónico de
// Protocol Buffers (protojson) para los mensajes proto.
//
// En Kratos v3 el códec "json" usa encoding/json de la librería estándar, que
// no conoce las reglas de protobuf (Timestamp como objeto, enums como números,
// oneof...). En lugar de reemplazar el códec global, estos encoders se
// registran de forma explícita en cada servidor HTTP.
package httpcodec

import (
	"bytes"
	"io"
	"mime"
	"net/http"
	"strings"

	kerrors "github.com/go-kratos/kratos/v3/errors"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var (
	marshal = protojson.MarshalOptions{
		UseProtoNames:   true, // snake_case, igual que en el .proto
		EmitUnpopulated: true, // los ceros (p. ej. available: 0) aparecen en la respuesta
	}
	unmarshal = protojson.UnmarshalOptions{DiscardUnknown: true}
)

// ServerOptions devuelve las opciones de servidor que activan protojson.
func ServerOptions() []khttp.ServerOption {
	return []khttp.ServerOption{
		khttp.RequestDecoder(RequestDecoder),
		khttp.ResponseEncoder(ResponseEncoder),
	}
}

// isJSON indica si una cabecera Content-Type/Accept pide JSON (o no pide nada).
func isJSON(header string) bool {
	if header == "" || header == "*/*" {
		return true
	}
	for _, part := range strings.Split(header, ",") {
		mt, _, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err != nil {
			continue
		}
		if mt == "*/*" || mt == "application/*" || strings.HasSuffix(mt, "/json") || strings.HasSuffix(mt, "+json") {
			return true
		}
	}
	return false
}

// RequestDecoder decodifica el cuerpo con protojson si el destino es un
// mensaje proto y el cuerpo es JSON; en otro caso delega en Kratos.
func RequestDecoder(r *http.Request, v any) error {
	m, ok := v.(proto.Message)
	if !ok || !isJSON(r.Header.Get("Content-Type")) {
		return khttp.DefaultRequestDecoder(r, v)
	}
	data, err := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(data))
	if err != nil {
		return kerrors.BadRequest("CODEC", err.Error())
	}
	if len(data) == 0 {
		return nil
	}
	if err := unmarshal.Unmarshal(data, m); err != nil {
		return kerrors.BadRequest("CODEC", "JSON inválido: "+err.Error())
	}
	return nil
}

// ResponseEncoder serializa con protojson los mensajes proto si el cliente
// acepta JSON; en otro caso delega en Kratos.
func ResponseEncoder(w http.ResponseWriter, r *http.Request, v any) error {
	m, ok := v.(proto.Message)
	if !ok || !isJSON(r.Header.Get("Accept")) {
		return khttp.DefaultResponseEncoder(w, r, v)
	}
	data, err := marshal.Marshal(m)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(data)
	return err
}
