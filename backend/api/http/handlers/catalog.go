package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/vishal1132/pikshipp/backend/internal/auth"
	"github.com/vishal1132/pikshipp/backend/internal/catalog"
	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// CatalogDeps are the dependencies for catalog handlers.
type CatalogDeps struct {
	Pickup  catalog.PickupService
	Product catalog.ProductService
}

// --- Pickup Locations ---

func ListPickupsHandler(d CatalogDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())
		items, err := d.Pickup.List(r.Context(), p.SellerID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, items)
	}
}

func CreatePickupHandler(d CatalogDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())
		var req catalog.PickupCreateRequest
		if err := decode(r, &req); err != nil {
			writeError(w, r, err)
			return
		}
		req.SellerID = p.SellerID
		item, err := d.Pickup.Create(r.Context(), req)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
	}
}

func GetPickupHandler(d CatalogDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())
		id, err := core.ParsePickupLocationID(chi.URLParam(r, "pickupID"))
		if err != nil {
			writeError(w, r, core.ErrInvalidArgument)
			return
		}
		item, err := d.Pickup.Get(r.Context(), p.SellerID, id)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}

// --- Products ---

func ListProductsHandler(d CatalogDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())
		items, err := d.Product.List(r.Context(), p.SellerID, catalog.ProductListQuery{Limit: 50})
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, items)
	}
}

func UpsertProductHandler(d CatalogDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := auth.MustPrincipalFrom(r.Context())
		var req catalog.ProductUpsertRequest
		if err := decode(r, &req); err != nil {
			writeError(w, r, err)
			return
		}
		req.SellerID = p.SellerID
		item, err := d.Product.Upsert(r.Context(), req)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}

func MountCatalog(r chi.Router, d CatalogDeps) {
	r.Get("/pickup-locations", ListPickupsHandler(d))
	r.Post("/pickup-locations", CreatePickupHandler(d))
	r.Get("/pickup-locations/{pickupID}", GetPickupHandler(d))
	r.Get("/products", ListProductsHandler(d))
	r.Put("/products", UpsertProductHandler(d))
}
