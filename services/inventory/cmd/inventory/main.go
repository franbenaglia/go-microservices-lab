// Command inventory arranca Inventory Service. Todas las dependencias se
// construyen aquí de forma explícita, sin generadores de inyección.
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
	"github.com/go-kratos/kratos/v3/middleware"

	inventoryv1 "github.com/franbenaglia/go-microservices-lab/api/gen/go/inventory/v1"
	"github.com/franbenaglia/go-microservices-lab/pkg/discovery"
	"github.com/franbenaglia/go-microservices-lab/pkg/platform"
	"github.com/franbenaglia/go-microservices-lab/pkg/postgres"
	"github.com/franbenaglia/go-microservices-lab/services/inventory/internal/biz"
	"github.com/franbenaglia/go-microservices-lab/services/inventory/internal/conf"
	"github.com/franbenaglia/go-microservices-lab/services/inventory/internal/data"
	"github.com/franbenaglia/go-microservices-lab/services/inventory/internal/lab"
	"github.com/franbenaglia/go-microservices-lab/services/inventory/internal/server"
	"github.com/franbenaglia/go-microservices-lab/services/inventory/internal/service"
	"github.com/franbenaglia/go-microservices-lab/services/inventory/migrations"
)

// ServiceName es el nombre lógico con el que se registra el servicio.
const ServiceName = "inventory-service"

// Version se sobrescribe en la compilación con -ldflags "-X main.Version=...".
var Version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "inventory:", err)
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

	health := platform.NewHealth(2 * time.Second)
	health.AddReadinessCheck("postgres", func(ctx context.Context) error { return pool.Ping(ctx) })

	uc := biz.NewInventoryUsecase(data.NewProductRepo(pool))
	svc := service.NewInventoryService(uc, id, logger)
	// Modo laboratorio: inyección de fallos (desactivada hasta que se configure)
	// y endpoint /admin/faults. Fuera de este modo no se registra nada.
	var extra []middleware.Middleware
	var injector *lab.Injector
	if platform.LabMode() {
		injector = lab.NewInjector(id.Instance)
		extra = append(extra, injector.Middleware())
		health.AddReadinessCheck("lab_faults", injector.ReadinessCheck)
	}

	httpSrv := server.NewHTTPServer(cfg.Server.HTTP, health, logger, extra...)
	inventoryv1.RegisterInventoryServiceHTTPServer(httpSrv, svc)
	if injector != nil {
		httpSrv.HandleFunc("/admin/faults", injector.Handler())
	}
	grpcSrv := server.NewGRPCServer(cfg.Server.GRPC, svc, logger, extra...)

	// Registro en Consul (si está configurado) con check de readiness.
	regOpts, _, err := discovery.Setup(cfg.Consul, httpSrv)
	if err != nil {
		return err
	}

	md := map[string]string{"instance": id.Instance, "hostname": platform.Hostname()}
	weight, err := platform.InstanceWeight()
	if err != nil {
		return err
	}
	if weight != "" {
		md["weight"] = weight
	}

	app := kratos.New(append(regOpts,
		kratos.ID(id.Instance),
		kratos.Name(id.Service),
		kratos.Version(id.Version),
		kratos.Metadata(md),
		kratos.Logger(logger),
		kratos.Server(httpSrv, grpcSrv),
		kratos.BeforeStop(func(context.Context) error {
			// Apagado ordenado: dejar de estar "ready" antes de cerrar servidores.
			health.SetDraining(true)
			logger.Info("apagando: instancia marcada como no preparada")
			return nil
		}),
	)...)
	logger.Info("arrancando",
		slog.String("http", cfg.Server.HTTP.Addr),
		slog.String("grpc", cfg.Server.GRPC.Addr),
		slog.Bool("consul", cfg.Consul.Enabled()),
		slog.String("weight", weight),
		slog.Bool("lab_mode", platform.LabMode()))
	return app.Run()
}
