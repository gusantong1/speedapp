package svc

import (
	"speedapp-packager/app/packager/internal/config"
	"speedapp-packager/app/packager/internal/keystore"
	"speedapp-packager/app/packager/internal/module/v1/packaging"
	"speedapp-packager/app/packager/internal/storage"
)

// ServiceContext 聚合打包服务的配置和各路由共用的业务依赖。
type ServiceContext struct {
	Config    *config.Config
	Packaging *packaging.Logic
}

func NewServiceContext(cfg *config.Config) (*ServiceContext, error) {
	if err := keystore.Ensure(cfg); err != nil {
		return nil, err
	}
	runner, err := packaging.NewRunner(cfg)
	if err != nil {
		return nil, err
	}
	store, err := storage.NewClient(cfg.Storage)
	if err != nil {
		return nil, err
	}
	return &ServiceContext{Config: cfg, Packaging: packaging.NewLogic(cfg, runner, store)}, nil
}
