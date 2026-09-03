package demo

import (
	"time"

	"github.com/agglayer/aggkit/bridgeservice"
	"github.com/agglayer/aggkit/bridgeservice/mocks"
	"github.com/agglayer/aggkit/bridgeservice/oapi"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/log"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/mock"
)

// SpecFirstPrefix is where the generated strict server is mounted. The
// generated routes already carry the /bridge/v1 prefix from the contract, so
// the full path is <SpecFirstPrefix>/bridge/v1/bridges -- deliberately a
// mirror of the live route one segment along, so the two can be curled back to
// back.
const SpecFirstPrefix = "/specfirst"

// demoReadTimeout bounds the request context the real handler builds. The
// service's own tests pass zero here, which produces an already-expired
// context; harmless for handlers whose dependencies ignore it, but not
// something to copy into anything that runs.
const demoReadTimeout = 10 * time.Second

// NewRouter builds one gin engine carrying both servers over the same rows:
//
//	GET /bridge/v1/bridges            -- the real BridgeService, unmodified
//	GET /specfirst/bridge/v1/bridges  -- the generated strict server
//
// The real service is instantiated exactly as bridgeservice's own test suite
// does it, with mocked syncers standing in for the databases. That matters for
// the demonstration: the left-hand endpoint is not a reimplementation or a
// simplification, it is the shipped handler, reached through the shipped
// routing, serialising with the shipped response types.
func NewRouter(bridges []*bridgesync.Bridge) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()

	registerRealBridgeService(engine, bridges)

	oapi.RegisterHandlers(
		engine.Group(SpecFirstPrefix),
		oapi.NewStrictHandler(NewSpecFirstServer(bridges), nil),
	)

	return engine
}

// registerRealBridgeService wires the production BridgeService onto router with
// mocked syncers that return the canned rows.
func registerRealBridgeService(router gin.IRouter, bridges []*bridgesync.Bridge) {
	bridgeL1 := &mocks.Bridger{}
	bridgeL1.On("GetBridgesPaged", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(bridges, len(bridges), nil)

	upgradeQuerier := &mocks.AgglayerManagerUpgradeQuerier{}
	upgradeQuerier.On("GetUpgradeBlock", mock.Anything, mock.Anything).Return(EtrogUpgradeBlock)

	service := bridgeservice.New(
		&bridgeservice.Config{
			Logger:       log.WithFields("module", "bridgeservice-specfirst-demo"),
			ReadTimeout:  demoReadTimeout,
			WriteTimeout: demoReadTimeout,
			NetworkID:    MainnetNetworkID,
		},
		upgradeQuerier,
		&mocks.L1InfoTreeSyncer{},
		&mocks.L2GERSyncer{},
		bridgeL1,
		&mocks.Claimer{},
		&mocks.Bridger{},
		&mocks.Claimer{},
	)

	service.RegisterRoutes(router)
}
