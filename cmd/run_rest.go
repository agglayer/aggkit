package main

import (
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/healthcheck"
	"github.com/agglayer/aggkit/log"
	"github.com/gin-gonic/gin"
)

// publicRoutesRegisterer registers read-only routes on the public REST server.
type publicRoutesRegisterer interface {
	RegisterPublicRoutes(gin.IRouter)
}

// adminRoutesRegisterer registers operator routes on the admin REST server.
type adminRoutesRegisterer interface {
	RegisterAdminRoutes(gin.IRouter)
}

// healthRoutesRegisterer registers a health check at "/" and "/health".
type healthRoutesRegisterer interface {
	RegisterHealthRoutes(gin.IRouter)
}

// restRoutes collects the routes every component registers on the PublicREST and
// AdminREST servers, and tracks whether each server has anything to serve.
type restRoutes struct {
	public          gin.IRouter
	admin           gin.IRouter
	publicHasRoutes bool
	adminHasRoutes  bool
	// runsComponents records that the node runs at least one component. Such a node always
	// serves the health routes, even when the components it runs register only admin routes.
	runsComponents bool
}

func newRESTRoutes(public, admin gin.IRouter, runsComponents bool) *restRoutes {
	return &restRoutes{public: public, admin: admin, runsComponents: runsComponents}
}

// addPublic registers the routes of a component that serves a single route set on the public server.
func (r *restRoutes) addPublic(c aggkitcommon.RoutesRegisterer) {
	c.RegisterRoutes(r.public)
	r.publicHasRoutes = true
}

// addPublicAPI registers the read-only routes of a component that splits its public and admin routes.
func (r *restRoutes) addPublicAPI(c publicRoutesRegisterer) {
	c.RegisterPublicRoutes(r.public)
	r.publicHasRoutes = true
}

// addAdminAPI registers the operator routes of a component on the admin server.
func (r *restRoutes) addAdminAPI(c adminRoutesRegisterer) {
	c.RegisterAdminRoutes(r.admin)
	r.adminHasRoutes = true
}

// addAggsender registers the aggsender read-only routes on the public server and its operator
// routes on the admin server, when the aggsender API is enabled.
func (r *restRoutes) addAggsender(c interface {
	publicRoutesRegisterer
	adminRoutesRegisterer
}, enabled bool) {
	if !enabled {
		return
	}
	r.addPublicAPI(c)
	r.addAdminAPI(c)
}

// addMultidownloader registers the multidownloader status route on the public server. Its
// operator routes are only registered on the admin server in developer mode.
func (r *restRoutes) addMultidownloader(c interface {
	publicRoutesRegisterer
	adminRoutesRegisterer
}, developerMode bool) {
	r.addPublicAPI(c)
	if developerMode {
		r.addAdminAPI(c)
	}
}

// registerHealth serves "/" and "/health" on the public server for every node that runs at
// least one component, so a node whose components register only admin routes (the aggchain
// proof generator) still answers health checks. It marks the public server as started. The
// bridge service health check is used when the bridge runs, otherwise the generic one.
func (r *restRoutes) registerHealth(bridge healthRoutesRegisterer) {
	if !r.runsComponents {
		return
	}
	if bridge != nil {
		bridge.RegisterHealthRoutes(r.public)
	} else {
		handler := gin.WrapH(healthcheck.NewHealthCheckHandler(log.WithFields("module", "health")))
		r.public.GET("/", handler)
		r.public.GET("/health", handler)
	}
	r.publicHasRoutes = true
}
