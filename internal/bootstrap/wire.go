package bootstrap

import (
	"context"
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/order"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/payment"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/auth"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/shared"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/user"
)

// wireFeatures registers every feature slice with the gin engine.
//
// THIS IS THE ONLY PLACE in the codebase that imports more than one feature
// package. That is by design — features stay decoupled, and cross-feature
// dependencies become explicit here.
func wireFeatures(
	engine *gin.Engine,
	pool *pgxpool.Pool,
	_ *redis.Client, // reserved for cache adapters
	authMgr *auth.Manager,
	_ *zap.Logger,
) {
	api := engine.Group("/api/v1")

	// ------------------------------------------------------------------
	// User feature
	// ------------------------------------------------------------------
	userRepo := user.NewPostgresRepo(pool)
	userHasher := user.NewBcryptHasher(0) // 0 = bcrypt.DefaultCost
	userSvc := user.NewService(userRepo, userHasher, authMgr)
	userHandler := user.NewHandler(userSvc)
	user.RegisterRoutes(api, userHandler, authMgr)

	// ------------------------------------------------------------------
	// Payment feature
	//
	// payment.Service exposes Charge() that structurally satisfies
	// order.PaymentCharger — Go's duck typing closes the loop.
	// ------------------------------------------------------------------
	paymentRepo := payment.NewPostgresRepo(pool)
	paymentGateway := payment.NewStubGateway() // swap for stripe/etc. later
	paymentSvc := payment.NewService(paymentRepo, paymentGateway)
	paymentHandler := payment.NewHandler(paymentSvc)
	payment.RegisterRoutes(api, paymentHandler, authMgr)

	// ------------------------------------------------------------------
	// Order feature
	//
	// order needs two cross-feature capabilities:
	//   - UserLookup    → adapted from user.Service.GetByID
	//   - PaymentCharger → satisfied directly by payment.Service.Charge
	//
	// We adapt user.Service.GetByID because order.UserLookup wants a bool,
	// not a *User. This little adapter is the cleanest place for the shape
	// change to live — see ADR 0001.
	// ------------------------------------------------------------------
	orderRepo := order.NewPostgresRepo(pool)
	userLookup := userLookupAdapter{svc: userSvc}
	orderSvc := order.NewService(orderRepo, userLookup, paymentSvc)
	orderHandler := order.NewHandler(orderSvc)
	order.RegisterRoutes(api, orderHandler, authMgr)
}

// ----------------------------------------------------------------------------
// Adapter: user.Service.GetByID → order.UserLookup
//
// This is bootstrap-only glue. It does not belong in either feature.
// ----------------------------------------------------------------------------

type userLookupAdapter struct {
	svc *user.Service
}

func (a userLookupAdapter) Exists(ctx context.Context, id shared.UserID) (bool, error) {
	_, err := a.svc.GetByID(ctx, id)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, user.ErrUserNotFound) {
		return false, nil
	}
	return false, err
}
