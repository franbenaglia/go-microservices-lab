package biz

import "errors"

// Errores de dominio. La capa service los traduce a errores de la API
// (inventoryv1.Error*), así el dominio no depende del transporte.
var (
	ErrInvalidArgument        = errors.New("argumento inválido")
	ErrProductNotFound        = errors.New("producto no encontrado")
	ErrSKUAlreadyExists       = errors.New("el SKU ya existe")
	ErrInsufficientStock      = errors.New("stock insuficiente")
	ErrReservationNotFound    = errors.New("reserva no encontrada")
	ErrIdempotencyKeyConflict = errors.New("clave de idempotencia reutilizada con otro contenido")
)
