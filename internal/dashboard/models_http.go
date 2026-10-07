package dashboard

import (
	"net/http"
)

func registerModelRoutes(mux *http.ServeMux, service *Service) {
	mux.HandleFunc("GET /v1/dashboard/models", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, service.models()) })
	save := func(w http.ResponseWriter, r *http.Request) {
		var input ModelProviderInput
		if !decode(w, r, &input) {
			return
		}
		provider, err := service.saveModelProvider(r.PathValue("id"), input)
		if err != nil {
			respondError(w, err)
			return
		}
		respond(w, 200, provider)
	}
	mux.HandleFunc("POST /v1/dashboard/models/providers", save)
	mux.HandleFunc("PUT /v1/dashboard/models/providers/{id}", save)
	mux.HandleFunc("DELETE /v1/dashboard/models/providers/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := service.deleteModelProvider(r.PathValue("id")); err != nil {
			respondError(w, err)
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("PUT /v1/dashboard/models/active", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Capability ModelCapability `json:"capability"`
			ProviderID string          `json:"provider_id"`
			ModelID    string          `json:"model_id"`
		}
		if !decode(w, r, &input) {
			return
		}
		configuration, err := service.selectModel(input.Capability, ModelSelection{ProviderID: input.ProviderID, ModelID: input.ModelID})
		if err != nil {
			respondError(w, err)
			return
		}
		respond(w, 200, configuration)
	})
	type draftRequest struct {
		ProviderID string              `json:"provider_id,omitempty"`
		Provider   *ModelProviderInput `json:"provider,omitempty"`
		Capability ModelCapability     `json:"capability,omitempty"`
		ModelID    string              `json:"model_id,omitempty"`
	}
	resolve := func(input draftRequest, discovery bool) (ModelProvider, error) {
		if input.Provider != nil {
			return service.modelDraft(input.ProviderID, *input.Provider, discovery)
		}
		return service.modelProvider(input.ProviderID)
	}
	mux.HandleFunc("POST /v1/dashboard/models/fetch", func(w http.ResponseWriter, r *http.Request) {
		var input draftRequest
		if !decode(w, r, &input) {
			return
		}
		provider, err := resolve(input, true)
		if err != nil {
			respondError(w, err)
			return
		}
		ids, err := fetchProviderModels(r.Context(), provider)
		if err != nil {
			respondError(w, err)
			return
		}
		respond(w, 200, map[string]any{"models": ids})
	})
	mux.HandleFunc("POST /v1/dashboard/models/test", func(w http.ResponseWriter, r *http.Request) {
		var input draftRequest
		if !decode(w, r, &input) {
			return
		}
		provider, err := resolve(input, false)
		if err != nil {
			respondError(w, err)
			return
		}
		result, err := testProviderModel(r.Context(), provider, input.Capability, input.ModelID)
		if err != nil {
			respondError(w, err)
			return
		}
		respond(w, 200, result)
	})
}
