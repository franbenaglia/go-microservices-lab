package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/hashicorp/consul/api"
	"google.golang.org/protobuf/types/known/timestamppb"

	ordersv1 "github.com/franbenaglia/go-microservices-lab/api/gen/go/orders/v1"
	"github.com/franbenaglia/go-microservices-lab/services/orders/internal/biz"
)

// overviewServices son los servicios que muestra el dashboard.
var overviewServices = []string{"orders-service", "inventory-service"}

// Consultas PromQL por servicio (etiqueta job = service.name).
var overviewQueries = map[string]string{
	"rps":    `sum by (job) (rate(server_requests_code_total[1m]))`,
	"errors": `sum by (job) (rate(server_requests_code_total{code=~"5.."}[1m]))`,
	"p95":    `histogram_quantile(0.95, sum by (le, job) (rate(server_requests_seconds_bucket[1m])))`,
	"total":  `sum by (job) (increase(server_requests_code_total[15m]))`,
}

// GetOverview compone el resumen del dashboard. Cada fuente es independiente:
// si Prometheus no responde, se devuelven las instancias igualmente.
func (s *LabService) GetOverview(ctx context.Context, _ *ordersv1.GetOverviewRequest) (*ordersv1.Overview, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	out := &ordersv1.Overview{GeneratedAt: timestamppb.Now()}

	var wg sync.WaitGroup
	wg.Go(func() { out.Services = s.instances(ctx) })
	wg.Go(func() { out.Metrics, out.MetricsError = s.metrics(ctx) })
	wg.Go(func() { out.RecentRejections = s.rejections(ctx) })
	wg.Wait()

	if b := s.resilience.Breaker(); b != nil {
		out.BreakerState = b.State()
	} else if s.resilience.Settings().Breaker.Enabled {
		out.BreakerState = "no observable (" + s.resilience.Settings().Breaker.Kind + ")"
	} else {
		out.BreakerState = "desactivado"
	}
	return out, nil
}

func (s *LabService) instances(ctx context.Context) []*ordersv1.ServiceStatus {
	if s.consul == nil {
		return nil
	}
	var out []*ordersv1.ServiceStatus
	for _, name := range overviewServices {
		entries, _, err := s.consul.Health().Service(name, "", false, (&api.QueryOptions{}).WithContext(ctx))
		svc := &ordersv1.ServiceStatus{Name: name}
		if err == nil {
			for _, e := range entries {
				in := &ordersv1.InstanceStatus{
					Id:       e.Service.ID,
					Address:  fmt.Sprintf("%s:%d", e.Service.Address, e.Service.Port),
					Version:  e.Service.Meta["version"],
					Hostname: e.Service.Meta["hostname"],
					Weight:   e.Service.Meta["weight"],
					Status:   api.HealthPassing,
				}
				for _, t := range e.Service.Tags {
					if len(t) > 8 && t[:8] == "version=" {
						in.Version = t[8:]
					}
				}
				for _, c := range e.Checks {
					if c.ServiceID == "" {
						continue // check del nodo
					}
					in.Checks = append(in.Checks, &ordersv1.CheckStatus{Name: c.Name, Type: c.Type, Status: c.Status})
					in.Status = worst(in.Status, c.Status)
				}
				if in.Status == api.HealthPassing {
					svc.Passing++
				}
				svc.Instances = append(svc.Instances, in)
			}
		}
		out = append(out, svc)
	}
	return out
}

func worst(a, b string) string {
	rank := map[string]int{api.HealthPassing: 0, api.HealthWarning: 1, api.HealthCritical: 2}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

func (s *LabService) metrics(ctx context.Context) ([]*ordersv1.ServiceMetrics, string) {
	if s.prom == nil {
		return nil, "Prometheus no configurado"
	}
	res := map[string]map[string]float64{}
	for k, q := range overviewQueries {
		v, err := s.prom.Vector(ctx, q, "job")
		if err != nil {
			return nil, err.Error()
		}
		res[k] = v
	}
	var out []*ordersv1.ServiceMetrics
	for _, name := range overviewServices {
		out = append(out, &ordersv1.ServiceMetrics{
			Service:          name,
			Rps:              res["rps"][name],
			ErrorsPerSecond:  res["errors"][name],
			P95Ms:            res["p95"][name] * 1000,
			RequestsLast_15M: res["total"][name],
		})
	}
	return out, ""
}

func (s *LabService) rejections(ctx context.Context) []*ordersv1.RecentRejection {
	if s.orders == nil {
		return nil
	}
	os, err := s.orders.ListOrders(ctx, biz.StatusRejected, 5, 0)
	if err != nil {
		return nil
	}
	out := make([]*ordersv1.RecentRejection, len(os))
	for i, o := range os {
		out[i] = &ordersv1.RecentRejection{
			OrderId: o.ID, RejectionCode: o.RejectionCode, Reason: o.RejectionReason, At: timestamppb.New(o.UpdatedAt),
		}
	}
	return out
}
