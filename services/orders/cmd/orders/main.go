// Command orders arranca Orders Service. Todas las dependencias se construyen
// aquí de forma explícita, sin generadores de inyección.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/go-kratos/kratos/v3"
	klog "github.com/go-kratos/kratos/v3/log"
	"github.com/go-kratos/kratos/v3/registry"
	"github.com/google/uuid"

	inventoryv1 "github.com/franbenaglia/go-microservices-lab/api/gen/go/inventory/v1"
	ordersv1 "github.com/franbenaglia/go-microservices-lab/api/gen/go/orders/v1"
	"github.com/franbenaglia/go-microservices-lab/pkg/discovery"
	"github.com/franbenaglia/go-microservices-lab/pkg/platform"
	"github.com/franbenaglia/go-microservices-lab/pkg/postgres"
	"github.com/franbenaglia/go-microservices-lab/services/orders/internal/biz"
	"github.com/franbenaglia/go-microservices-lab/services/orders/internal/conf"
	"github.com/franbenaglia/go-microservices-lab/services/orders/internal/data"
	"github.com/franbenaglia/go-microservices-lab/services/orders/internal/server"
	"github.com/franbenaglia/go-microservices-lab/services/orders/internal/service"
	"github.com/franbenaglia/go-microservices-lab/services/orders/migrations"
)

// ServiceName es el nombre lógico con el que se registra el servicio.
const ServiceName = "orders-service"

// Version se sobrescribe en la compilación con -ldflags "-X main.Version=...".
var Version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "orders:", err)
		os.Exit(1)
	}
}

func run() error {
	confPath := flag.String("conf", "configs/config.yaml", "ruta del archivo de configuración")
	healthcheck := flag.String("healthcheck", "", "si se indica una URL, la consulta y termina (healthcheck de Docker)")
	flag.Parse()
	if *healthcheck != "" {
		return platform.RunHealthcheck(*healthcheck)
	}

	var cfg conf.Config
	c, err := platform.LoadConfig(*confPath, &cfg)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("configuración inválida: %w", err)
	}

	id := platform.Identity{Service: ServiceName, Instance: platform.InstanceID(ServiceName), Version: Version}
	logger, _, err := platform.NewLogger(id, cfg.Log.Level)
	if err != nil {
		return err
	}
	klog.SetDefault(logger)

	initCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := postgres.NewPool(initCtx, postgres.Options{
		DSN:             cfg.Data.DSN,
		MaxConns:        cfg.Data.MaxConns,
		ConnectTimeout:  5 * time.Second,
		ConnectAttempts: 30,
	})
	if err != nil {
		return err
	}
	defer pool.Close()
	if cfg.Data.AutoMigrate {
		if err := postgres.Migrate(initCtx, pool, migrations.FS); err != nil {
			return err
		}
		logger.Info("migraciones aplicadas")
	}

	// Readiness solo depende de la base de datos propia: si Inventory cae,
	// Orders sigue atendiendo (rechaza pedidos de forma controlada) en lugar
	// de desaparecer del balanceo.
	health := platform.NewHealth(2 * time.Second)
	health.AddReadinessCheck("postgres", func(ctx context.Context) error { return pool.Ping(ctx) })

	// El servidor HTTP se crea antes que el resto para conocer su endpoint:
	// el registry de Consul lo necesita para el check de readiness y, a la
	// vez, el cliente gRPC necesita el registry para descubrir Inventory.
	httpSrv := server.NewHTTPServer(cfg.Server.HTTP, health, logger)
	regOpts, reg, err := discovery.Setup(cfg.Consul, httpSrv)
	if err != nil {
		return err
	}
	var disc registry.Discovery // nil explícito si no hay Consul (evita interfaz con puntero nil)
	if reg != nil {
		disc = reg
	}
	conn, err := server.NewInventoryConn(context.Background(), cfg.Inventory, disc, logger)
	if err != nil {
		return fmt.Errorf("cliente de inventory: %w", err)
	}
	defer conn.Close()

	newID := func() (string, error) {
		u, err := uuid.NewV7()
		return u.String(), err
	}
	invClient := inventoryv1.NewInventoryServiceClient(conn)
	inv := data.NewInventoryClient(invClient)
	uc := biz.NewOrderUsecase(data.NewOrderRepo(pool), inv, newID, cfg.Inventory.CompensationTimeout.Std())
	svc := service.NewOrdersService(uc, logger)
	ordersv1.RegisterOrdersServiceHTTPServer(httpSrv, svc)
	lab := service.NewLabService(invClient)
	ordersv1.RegisterLabServiceHTTPServer(httpSrv, lab)
	grpcSrv := server.NewGRPCServer(cfg.Server.GRPC, svc, logger)

	app := kratos.New(append(regOpts,
		kratos.ID(id.Instance),
		kratos.Name(id.Service),
		kratos.Version(id.Version),
		kratos.Metadata(map[string]string{"instance": id.Instance, "hostname": platform.Hostname()}),
		kratos.Logger(logger),
		kratos.Server(httpSrv, grpcSrv),
		kratos.BeforeStop(func(context.Context) error {
			health.SetDraining(true)
			logger.Info("apagando: instancia marcada como no preparada")
			return nil
		}),
	)...)
	logger.Info("arrancando",
		slog.String("http", cfg.Server.HTTP.Addr),
		slog.String("grpc", cfg.Server.GRPC.Addr),
		slog.String("inventory", cfg.Inventory.Endpoint),
		slog.Bool("consul", cfg.Consul.Enabled()))
	return app.Run()
}
