// Command inventory arranca Inventory Service. Todas las dependencias se
// construyen aquí de forma explícita, sin generadores de inyección.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"time"

	"github.com/go-kratos/kratos/v3"
	klog "github.com/go-kratos/kratos/v3/log"
	"github.com/go-kratos/kratos/v3/middleware"

	inventoryv1 "github.com/franbenaglia/go-microservices-lab/api/gen/go/inventory/v1"
	"github.com/franbenaglia/go-microservices-lab/pkg/discovery"
	"github.com/franbenaglia/go-microservices-lab/pkg/dynconfig"
	"github.com/franbenaglia/go-microservices-lab/pkg/platform"
	"github.com/franbenaglia/go-microservices-lab/pkg/postgres"
	"github.com/franbenaglia/go-microservices-lab/pkg/telemetry"
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
	logger, logLevel, err := platform.NewLogger(id, cfg.Log.Level)
	if err != nil {
		return err
	}
	klog.SetDefault(logger)

	initCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	tel, err := telemetry.Setup(initCtx, cfg.Telemetry, id)
	if err != nil {
		return err
	}
	defer shutdownTelemetry(tel, logger)
	serverTel, err := tel.ServerMiddlewares()
	if err != nil {
		return err
	}

	pool, err := postgres.NewPool(initCtx, postgres.Options{
		DSN:             cfg.Data.DSN,
		MaxConns:        cfg.Data.MaxConns,
		ConnectTimeout:  5 * time.Second,
		ConnectAttempts: 30,
		Tracer:          postgres.NewTracer(),
	})
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := postgres.RecordStats(pool); err != nil {
		return err
	}
	if cfg.Data.AutoMigrate {
		if err := postgres.Migrate(initCtx, pool, migrations.FS); err != nil {
			return err
		}
		logger.Info("migraciones aplicadas")
	}

	health := platform.NewHealth(2 * time.Second)
	health.AddReadinessCheck("postgres", func(ctx context.Context) error { return pool.Ping(ctx) })
	if err := tel.RegisterReadiness(health.ReadyFunc()); err != nil {
		return err
	}

	uc := biz.NewInventoryUsecase(data.NewProductRepo(pool))
	svc, err := service.NewInventoryService(uc, id, logger, tel.Meter)
	if err != nil {
		return err
	}
	// Modo laboratorio: inyección de fallos (desactivada hasta que se configure)
	// y endpoint /admin/faults. Fuera de este modo no se registra nada.
	var extra []middleware.Middleware
	var injector *lab.Injector
	if platform.LabMode() {
		injector = lab.NewInjector(id.Instance)
		extra = append(extra, injector.Middleware())
		health.AddReadinessCheck("lab_faults", injector.ReadinessCheck)
	}

	mws := server.Middlewares{Telemetry: serverTel, Extra: extra}
	httpSrv := server.NewHTTPServer(cfg.Server.HTTP, health, logger, mws)
	inventoryv1.RegisterInventoryServiceHTTPServer(httpSrv, svc)
	if injector != nil {
		httpSrv.Handle("/admin/faults", tel.WrapHTTP("admin.faults", injector.Handler()))
	}
	grpcSrv := server.NewGRPCServer(cfg.Server.GRPC, svc, logger, mws)

	// Configuración dinámica (Consul KV): nivel de log y, en modo laboratorio,
	// los fallos simulados para TODAS las réplicas. Los fallos solo se aplican
	// si cambian en el documento, para no pisar un ajuste hecho por /admin/faults
	// en una réplica concreta al cambiar otro parámetro.
	consulClient, err := discovery.NewClient(cfg.Consul)
	if err != nil {
		return err
	}
	dyn, err := dynconfig.New(dynconfig.Options[conf.Dynamic]{
		Service:  ServiceName,
		Client:   consulClient,
		Defaults: cfg.Dynamic(),
		Validate: func(_, next *conf.Dynamic) error { return next.Validate() },
		Apply: func(prev, next *conf.Dynamic) {
			_ = platform.SetLogLevel(logLevel, next.Log.Level) // ya validado
			if injector != nil && !reflect.DeepEqual(prev.Lab.Faults, next.Lab.Faults) {
				_ = injector.Set(next.Lab.Faults) // ya validado
				logger.Warn("fallos de laboratorio actualizados desde Consul KV", slog.Any("faults", next.Lab.Faults))
			}
		},
		Logger: logger,
		Meter:  tel.Meter,
	})
	if err != nil {
		return err
	}
	if err := dyn.Start(initCtx); err != nil {
		return fmt.Errorf("configuración dinámica: %w", err)
	}
	defer dyn.Close()
	if platform.LabMode() {
		httpSrv.Handle("/admin/config", tel.WrapHTTP("admin.config", dyn.Handler(id.Instance, cfg.Static(), conf.RestartRequired)))
	}

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
		slog.Bool("lab_mode", platform.LabMode()),
		slog.Bool("telemetry", cfg.Telemetry.Enabled()))
	return app.Run()
}

// shutdownTelemetry vacía las trazas y métricas pendientes al terminar.
func shutdownTelemetry(tel *telemetry.Telemetry, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tel.Shutdown(ctx); err != nil {
		logger.Warn("cierre de telemetría", slog.Any("error", err))
	}
}
