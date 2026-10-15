package service

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	kerrors "github.com/go-kratos/kratos/v3/errors"

	inventoryv1 "github.com/franbenaglia/go-microservices-lab/api/gen/go/inventory/v1"
	ordersv1 "github.com/franbenaglia/go-microservices-lab/api/gen/go/orders/v1"
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

	inventory inventoryv1.InventoryServiceClient
}

var (
	_ ordersv1.LabServiceServer     = (*LabService)(nil)
	_ ordersv1.LabServiceHTTPServer = (*LabService)(nil)
)

// NewLabService construye el servicio de laboratorio.
func NewLabService(inv inventoryv1.InventoryServiceClient) *LabService {
	return &LabService{inventory: inv}
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
	resp := &ordersv1.ProbeInventoryResponse{Sequence: make([]string, 0, count)}
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
