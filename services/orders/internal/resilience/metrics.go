package resilience

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// stateValue codifica el estado del breaker para la métrica de tipo gauge.
var stateValue = map[string]int64{"closed": 0, "half-open": 1, "open": 2}

// RegisterMetrics publica el estado del circuit breaker y los contadores de
// reintentos. Son instrumentos observables: se leen al exportar (cada
// metric_interval) sin añadir coste al camino de cada llamada.
//
//	circuit_breaker_state{breaker}                   0=closed 1=half-open 2=open
//	circuit_breaker_rejected_total{breaker}          rechazos locales (OPEN / HALF-OPEN lleno)
//	circuit_breaker_calls_total{breaker,result}      intentos permitidos por resultado
//	client_retry_events_total{event}                 attempts|retries|recovered|exhausted|not_retryable
//
// Las transiciones se cuentan como evento en circuit_breaker_transitions_total
// (ver TransitionCounter).
func (r *Resilience) RegisterMetrics(m metric.Meter) error {
	attrs := metric.WithAttributes(attribute.String("breaker", "inventory-service"))
	var insts []metric.Observable

	state, err := m.Int64ObservableGauge("circuit_breaker_state",
		metric.WithDescription("Estado del circuit breaker: 0=closed, 1=half-open, 2=open"))
	if err != nil {
		return err
	}
	rejected, err := m.Int64ObservableCounter("circuit_breaker_rejected_total",
		metric.WithDescription("Peticiones rechazadas localmente por el circuit breaker"))
	if err != nil {
		return err
	}
	calls, err := m.Int64ObservableCounter("circuit_breaker_calls_total",
		metric.WithDescription("Intentos permitidos por el breaker, por resultado"))
	if err != nil {
		return err
	}
	retries, err := m.Int64ObservableCounter("client_retry_events_total",
		metric.WithDescription("Eventos del middleware de reintentos"))
	if err != nil {
		return err
	}
	insts = append(insts, retries)
	if r.Breaker != nil {
		insts = append(insts, state, rejected, calls)
	}

	_, err = m.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		if b := r.Breaker; b != nil {
			snap := b.Snapshot()
			o.ObserveInt64(state, stateValue[snap.State], attrs)
			o.ObserveInt64(rejected, int64(snap.Rejected), attrs)
			o.ObserveInt64(calls, int64(snap.Successes), metric.WithAttributes(
				attribute.String("breaker", "inventory-service"), attribute.String("result", "success")))
			o.ObserveInt64(calls, int64(snap.Failures), metric.WithAttributes(
				attribute.String("breaker", "inventory-service"), attribute.String("result", "failure")))
		}
		st := r.Retrier.Stats()
		for ev, v := range map[string]uint64{
			"attempts": st.Attempts, "retries": st.Retries, "recovered": st.Recovered,
			"exhausted": st.Exhausted, "not_retryable": st.NotRetryable,
		} {
			o.ObserveInt64(retries, int64(v), metric.WithAttributes(attribute.String("event", ev)))
		}
		return nil
	}, insts...)
	return err
}

// TransitionCounter devuelve una función para contar cada cambio de estado.
func TransitionCounter(m metric.Meter) (func(from, to string), error) {
	c, err := m.Int64Counter("circuit_breaker_transitions_total",
		metric.WithDescription("Cambios de estado del circuit breaker"))
	if err != nil {
		return nil, err
	}
	return func(from, to string) {
		c.Add(context.Background(), 1, metric.WithAttributes(
			attribute.String("breaker", "inventory-service"),
			attribute.String("from", from), attribute.String("to", to)))
	}, nil
}
