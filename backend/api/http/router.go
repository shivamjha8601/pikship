package http

import (
	"log/slog"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vishal1132/pikshipp/backend/api/http/handlers"
	"github.com/vishal1132/pikshipp/backend/internal/auth"
	"github.com/vishal1132/pikshipp/backend/internal/buyerexp"
	"github.com/vishal1132/pikshipp/backend/internal/catalog"
	"github.com/vishal1132/pikshipp/backend/internal/contracts"
	"github.com/vishal1132/pikshipp/backend/internal/idempotency"
	"github.com/vishal1132/pikshipp/backend/internal/identity"
	"github.com/vishal1132/pikshipp/backend/internal/limits"
	"github.com/vishal1132/pikshipp/backend/internal/ndr"
	"github.com/vishal1132/pikshipp/backend/internal/observability/metrics"
	"github.com/vishal1132/pikshipp/backend/internal/orders"
	"github.com/vishal1132/pikshipp/backend/internal/reports"
	"github.com/vishal1132/pikshipp/backend/internal/seller"
	"github.com/vishal1132/pikshipp/backend/internal/shipments"
	"github.com/vishal1132/pikshipp/backend/internal/tracking"
	"github.com/vishal1132/pikshipp/backend/internal/wallet"
)

// AppDeps carries all wired-up services needed by the HTTP layer.
type AppDeps struct {
	Pools     Pools
	Log       *slog.Logger
	Auth      auth.Authenticator
	Identity  identity.Service
	Seller    seller.Service
	Pickup    catalog.PickupService
	Product   catalog.ProductService
	Orders    orders.Service
	Shipments shipments.Service
	Wallet    wallet.Service
	Tracking  tracking.Service
	BuyerExp  buyerexp.Service
	NDR       ndr.Service
	Reports   reports.Service
	Contracts contracts.Service
	Limits    limits.Guard
	AppPool   *pgxpool.Pool
	DevMode   bool // exposes /v1/auth/dev-login when true

	// Google OAuth — when Google is nil, /v1/auth/google/* return 503.
	Google              handlers.GoogleOAuthAdapter
	GoogleFrontendURL   string
}

// NewAppRouter builds the full chi router with all middleware and routes.
func NewAppRouter(deps AppDeps, requestTimeout time.Duration) chi.Router {
	r := chi.NewRouter()

	// Platform middleware (order matters)
	r.Use(chimiddleware.RealIP)
	r.Use(RequestID)
	r.Use(Recover(deps.Log))
	r.Use(InjectLogger(deps.Log))
	r.Use(RequestLogger)
	r.Use(SecurityHeaders)
	r.Use(Timeout(requestTimeout))
	r.Use(chimiddleware.Compress(5))

	// Health + metrics (no auth)
	r.Get("/healthz", healthz)
	r.Get("/readyz", readyz(deps.Pools, requestTimeout))
	r.Handle("/metrics", metrics.Handler())

	// Public tracking + carrier webhooks (no auth)
	handlers.MountPublicTracking(r, handlers.TrackingDeps{
		Tracking: deps.Tracking,
		BuyerExp: deps.BuyerExp,
		NDR:      deps.NDR,
	})
	handlers.MountWebhooks(r, handlers.WebhookDeps{
		Tracking: deps.Tracking,
	})

	// /v1 routes — public onboarding first, then auth, then seller scope.
	onboardingDeps := handlers.OnboardingDeps{
		Identity:          deps.Identity,
		Seller:            deps.Seller,
		Auth:              deps.Auth,
		DevMode:           deps.DevMode,
		Google:            deps.Google,
		GoogleFrontendURL: deps.GoogleFrontendURL,
	}
	r.Route("/v1", func(r chi.Router) {
		// Public (no auth)
		handlers.MountOnboarding(r, onboardingDeps)

		// Authenticated
		r.Group(func(r chi.Router) {
			r.Use(auth.Middleware(deps.Auth, deps.Log))

			handlers.MountIdentity(r, handlers.IdentityDeps{
				Identity: deps.Identity,
				Auth:     deps.Auth,
			})
			handlers.MountSellerProvisioning(r, onboardingDeps)

			adminDeps := handlers.AdminDeps{
				Seller:    deps.Seller,
				Contracts: deps.Contracts,
				Limits:    deps.Limits,
			}
			handlers.MountAdmin(r, adminDeps) // /admin/sellers/{id}/upgrade

			// Seller-scoped routes
			r.Group(func(r chi.Router) {
				r.Use(SellerScope)
				r.Use(idempotency.Middleware(deps.AppPool))

				handlers.MountSeller(r, handlers.SellerDeps{Seller: deps.Seller})
				handlers.MountSellerContractViews(r, adminDeps)
				handlers.MountCatalog(r, handlers.CatalogDeps{
					Pickup:  deps.Pickup,
					Product: deps.Product,
				})
				handlers.MountOrders(r, handlers.OrdersDeps{
					Orders: deps.Orders,
					Limits: deps.Limits,
				})
				handlers.MountShipments(r, handlers.ShipmentDeps{
					Shipments: deps.Shipments,
					Wallet:    deps.Wallet,
					Reports:   deps.Reports,
				})
				handlers.MountTracking(r, handlers.TrackingDeps{
					Tracking: deps.Tracking,
					BuyerExp: deps.BuyerExp,
					NDR:      deps.NDR,
				})
			})
		})
	})

	return r
}
