package service

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	kerrors "github.com/go-kratos/kratos/v3/errors"
	"google.golang.org/protobuf/types/known/timestamppb"

	inventoryv1 "github.com/franbenaglia/go-microservices-lab/api/gen/go/inventory/v1"
	ordersv1 "github.com/franbenaglia/go-microservices-lab/api/gen/go/orders/v1"
	"github.com/franbenaglia/go-microservices-lab/services/orders/internal/resilience"
)

const (
	maxProbeCount    = 1000
	maxProbeInterval = 1000
	defaultProbes    = 20
)

// LabService expone diagnósticos del laboratorio. Usa directamente el cliente
// gRPC de Inventory (el mismo que usa la lógica de negocio, con el mismo
// descubrimiento y balanceo), pero no forma parte del dominio de pedidos.
type LabService struct {
	ordersv1.UnimplementedLabServiceServer

	inventory   inventoryv1.InventoryServiceClient
	balancer    string
	resilience  *resilience.Resilience
	callTimeout time.Duration
}

var (
	_ ordersv1.LabServiceServer     = (*LabService)(nil)
	_ ordersv1.LabServiceHTTPServer = (*LabService)(nil)
)

// NewLabService construye el servicio de laboratorio.
func NewLabService(inv inventoryv1.InventoryServiceClient, balancer string, res *resilience.Resilience, callTimeout time.Duration) *LabService {
	return &LabService{inventory: inv, balancer: balancer, resilience: res, callTimeout: callTimeout}
}

// GetResilience devuelve el estado del breaker y de los reintentos. Solo se
// informa del estado del breaker si la implementación lo expone (classic): el
// breaker SRE de Kratos no tiene API de inspección y no se inventa una.
func (s *LabService) GetResilience(context.Context, *ordersv1.GetResilienceRequest) (*ordersv1.ResilienceStatus, error) {
	c := s.resilience.Settings
	out := &ordersv1.ResilienceStatus{
		CallTimeout: s.callTimeout.String(),
		Balancer:    s.balancer,
		Breaker: &ordersv1.BreakerStatus{
			Enabled:                      c.Breaker.Enabled,
			Kind:                         c.Breaker.Kind,
			FailureRatio:                 c.Breaker.FailureRatio,
			MinRequests:                  c.Breaker.MinRequests,
			ConsecutiveFailuresThreshold: c.Breaker.ConsecutiveFailures,
			Window:                       c.Breaker.Window.Std().String(),
			OpenTimeout:                  c.Breaker.OpenTimeout.Std().String(),
			HalfOpenMaxRequests:          c.Breaker.HalfOpenMaxRequests,
		},
	}
	if b := s.resilience.Breaker; b != nil {
		snap := b.Snapshot()
		bs := out.Breaker
		bs.StateObservable = true
		bs.State = snap.State
		bs.Requests, bs.TotalSuccesses, bs.TotalFailures = snap.Requests, snap.TotalSuccesses, snap.TotalFailures
		bs.ConsecutiveFailures, bs.ConsecutiveSuccesses = snap.ConsecutiveFailures, snap.ConsecutiveSuccesses
		bs.Allowed, bs.Rejected, bs.Successes, bs.Failures = snap.Allowed, snap.Rejected, snap.Successes, snap.Failures
		for _, t := range snap.Transitions {
			bs.Transitions = append(bs.Transitions, &ordersv1.BreakerTransition{From: t.From, To: t.To, At: timestamppb.New(t.At)})
		}
	}
	p := s.resilience.Retrier.Policy()
	st := s.resilience.Retrier.Stats()
	out.Retry = &ordersv1.RetryStatus{
		Enabled: p.Enabled, MaxAttempts: int32(p.MaxAttempts),
		BaseBackoff: p.BaseBackoff.String(), MaxBackoff: p.MaxBackoff.String(), AttemptTimeout: p.AttemptTimeout.String(),
		IdempotentOperations: p.Idempotent, KeyRequiredOperations: p.KeyRequired,
		Calls: st.Calls, Attempts: st.Attempts, Retries: st.Retries,
		Recovered: st.Recovered, Exhausted: st.Exhausted, NotRetryable: st.NotRetryable,
	}
	return out, nil
}

// ProbeInventory envía llamadas Diagnose consecutivas y resume la distribución.
func (s *LabService) ProbeInventory(ctx context.Context, req *ordersv1.ProbeInventoryRequest) (*ordersv1.ProbeInventoryResponse, error) {
	count := int(req.GetCount())
	if count == 0 {
		count = defaultProbes
	}
	if count < 0 || count > maxProbeCount {
		return nil, ordersv1.ErrorInvalidArgument("count debe estar entre 1 y %d", maxProbeCount)
	}
	interval := time.Duration(req.GetIntervalMs()) * time.Millisecond
	if req.GetIntervalMs() < 0 || req.GetIntervalMs() > maxProbeInterval {
		return nil, ordersv1.ErrorInvalidArgument("interval_ms debe estar entre 0 y %d", maxProbeInterval)
	}

	byInstance := map[string]int32{}
	byError := map[string]int32{}
	resp := &ordersv1.ProbeInventoryResponse{Sequence: make([]string, 0, count), Balancer: s.balancer}
	var totalLatency, maxLatency time.Duration

	for i := range count {
		if err := ctx.Err(); err != nil {
			return nil, kerrors.ClientClosed("CANCELED", "sondeo cancelado")
		}
		start := time.Now()
		d, err := s.inventory.Diagnose(ctx, &inventoryv1.DiagnoseRequest{})
		lat := time.Since(start)
		totalLatency += lat
		maxLatency = max(maxLatency, lat)
		resp.Total++
		if err != nil {
			resp.Failed++
			byError[errorKey(err)]++
			resp.Sequence = append(resp.Sequence, "")
		} else {
			resp.Succeeded++
			byInstance[d.GetInstanceId()]++
			resp.Sequence = append(resp.Sequence, d.GetInstanceId())
		}
		if interval > 0 && i < count-1 {
			select {
			case <-time.After(interval):
			case <-ctx.Done():
			}
		}
	}

	for _, id := range sortedKeys(byInstance) {
		resp.ByInstance = append(resp.ByInstance, &ordersv1.InstanceCount{InstanceId: id, Count: byInstance[id]})
	}
	for _, r := range sortedKeys(byError) {
		resp.Errors = append(resp.Errors, &ordersv1.ErrorCount{Reason: r, Count: byError[r]})
	}
	resp.AvgLatencyMs = ms(totalLatency) / float64(resp.Total)
	resp.MaxLatencyMs = ms(maxLatency)
	return resp, nil
}

// errorKey agrupa errores por razón de Kratos o, si no hay, por código.
func errorKey(err error) string {
	se := kerrors.FromError(err)
	if se.Reason != "" {
		return se.Reason
	}
	return "HTTP_" + strconv.Itoa(int(se.Code))
}

func sortedKeys(m map[string]int32) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, strings.Compare)
	return keys
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
