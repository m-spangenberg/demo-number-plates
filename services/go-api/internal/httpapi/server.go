package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"demo-number-plates/services/go-api/internal/config"
	"demo-number-plates/services/go-api/internal/model"
	"demo-number-plates/services/go-api/internal/service"
	"demo-number-plates/services/go-api/internal/storage"

	"github.com/graphql-go/graphql"
	graphqlhandler "github.com/graphql-go/handler"
)

type Server struct {
	httpServer *http.Server
	query      *service.QueryService
	redis      *storage.RedisStore
	cfg        config.Config
	isReady    func() bool
	initError  func() string
}

func New(cfg config.Config, query *service.QueryService, redis *storage.RedisStore, isReady func() bool, initError func() string) (*Server, error) {
	server := &Server{
		query:     query,
		redis:     redis,
		cfg:       cfg,
		isReady:   isReady,
		initError: initError,
	}

	schema, err := server.buildGraphQLSchema()
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", server.handleLiveness)
	mux.HandleFunc("/readyz", server.handleReadiness)
	mux.Handle("/find", server.lookupMiddleware(http.HandlerFunc(server.handleFind)))
	mux.Handle("/graphql", server.lookupMiddleware(server.newGraphQLHandler(schema)))

	server.httpServer = &http.Server{
		Addr:              cfg.ServerAddr,
		Handler:           mux,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
	}
	return server, nil
}

func (s *Server) ListenAndServe() error {
	err := s.httpServer.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) handleLiveness(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{"status": "alive"})
}

func (s *Server) handleReadiness(writer http.ResponseWriter, _ *http.Request) {
	if message := s.initError(); message != "" {
		writeJSON(writer, http.StatusInternalServerError, map[string]any{"status": "error", "error": message})
		return
	}
	if !s.isReady() {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]any{"status": "starting"})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"status": "ready"})
}

func (s *Server) handleFind(writer http.ResponseWriter, request *http.Request) {
	lookup, apiErr := s.query.Lookup(request.Context(), model.LookupRequest{
		Type:            request.URL.Query().Get("t"),
		Query:           request.URL.Query().Get("q"),
		SuggestionLimit: 5,
	})
	if apiErr != nil {
		writeJSON(writer, apiErr.Code, apiErr)
		return
	}
	writeJSON(writer, http.StatusOK, lookup)
}

func (s *Server) lookupMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !s.isReady() {
			writeJSON(writer, http.StatusServiceUnavailable, map[string]any{"message": "service is still initializing"})
			return
		}

		apiKey := strings.TrimSpace(request.Header.Get("X-API-Key"))
		if apiKey == "" {
			authorization := strings.TrimSpace(request.Header.Get("Authorization"))
			if strings.HasPrefix(strings.ToLower(authorization), "apikey ") {
				apiKey = strings.TrimSpace(authorization[len("apikey "):])
			}
		}
		if _, ok := s.cfg.APIKeys[apiKey]; !ok {
			writeJSON(writer, http.StatusUnauthorized, map[string]any{"message": "missing or invalid API key"})
			return
		}

		allowed, remaining, err := s.redis.AllowRequest(request.Context(), apiKey)
		if err != nil {
			writeJSON(writer, http.StatusServiceUnavailable, map[string]any{"message": "rate limiter unavailable", "detail": err.Error()})
			return
		}
		writer.Header().Set("X-RateLimit-Limit", strconv.Itoa(s.cfg.RateLimitRequests))
		writer.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
		if !allowed {
			writeJSON(writer, http.StatusTooManyRequests, map[string]any{"message": "rate limit exceeded"})
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func (s *Server) buildGraphQLSchema() (graphql.Schema, error) {
	lookupType := graphql.NewObject(graphql.ObjectConfig{
		Name: "LookupResult",
		Fields: graphql.Fields{
			"input":       &graphql.Field{Type: graphql.String},
			"type":        &graphql.Field{Type: graphql.String},
			"normalized":  &graphql.Field{Type: graphql.String},
			"available":   &graphql.Field{Type: graphql.Boolean},
			"suggestions": &graphql.Field{Type: graphql.NewList(graphql.String)},
		},
	})

	queryType := graphql.NewObject(graphql.ObjectConfig{
		Name: "Query",
		Fields: graphql.Fields{
			"lookupPlate": &graphql.Field{
				Type: lookupType,
				Args: graphql.FieldConfigArgument{
					"type":  &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
					"query": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
				},
				Resolve: func(params graphql.ResolveParams) (any, error) {
					result, apiErr := s.query.Lookup(params.Context, model.LookupRequest{
						Type:            params.Args["type"].(string),
						Query:           params.Args["query"].(string),
						SuggestionLimit: 5,
					})
					if apiErr != nil {
						return nil, errors.New(apiErr.Message)
					}
					return result, nil
				},
			},
			"suggestVanity": &graphql.Field{
				Type: graphql.NewList(graphql.String),
				Args: graphql.FieldConfigArgument{
					"prefix": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
					"limit":  &graphql.ArgumentConfig{Type: graphql.Int},
				},
				Resolve: func(params graphql.ResolveParams) (any, error) {
					limit, _ := params.Args["limit"].(int)
					if limit <= 0 {
						limit = 5
					}
					return s.redis.SuggestVanity(params.Context, params.Args["prefix"].(string), limit)
				},
			},
		},
	})

	return graphql.NewSchema(graphql.SchemaConfig{Query: queryType})
}

func (s *Server) newGraphQLHandler(schema graphql.Schema) http.Handler {
	return graphqlhandler.New(&graphqlhandler.Config{
		Schema: &schema,
		Pretty: true,
	})
}

func writeJSON(writer http.ResponseWriter, statusCode int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(statusCode)
	_ = json.NewEncoder(writer).Encode(value)
}
