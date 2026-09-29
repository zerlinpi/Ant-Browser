package gatewayservice

import (
	"net/http"

	"github.com/zerlinpi/Ant-Browser/server/platform/httpx"
)

// Feature files register their routes, options, error mappings and
// middleware from init functions, so gateway.go stays a stable core and
// features do not need to edit it.

type (
	// routeRegistrar adds a feature's routes. Routes on public skip bearer
	// authentication; routes on protected run after it.
	routeRegistrar func(g *Gateway, public, protected *http.ServeMux)
	// optionHandler consumes a NewWithInfrastructure option it recognizes
	// and reports whether it did.
	optionHandler func(g *Gateway, option interface{}) bool
	// errorMapper translates a feature's service error into a problem. It
	// may set response headers such as Retry-After.
	errorMapper func(g *Gateway, w http.ResponseWriter, r *http.Request, err error) (httpx.Problem, bool)
	// protectedMiddleware wraps every authenticated route and runs after
	// the principal is known.
	protectedMiddleware func(g *Gateway, next http.Handler) http.Handler
)

var (
	routeRegistrars      []routeRegistrar
	optionHandlers       []optionHandler
	errorMappers         []errorMapper
	protectedMiddlewares []protectedMiddleware
)

func registerRoutes(registrar routeRegistrar) { routeRegistrars = append(routeRegistrars, registrar) }
func registerOption(handler optionHandler)    { optionHandlers = append(optionHandlers, handler) }
func registerErrorMapper(mapper errorMapper)  { errorMappers = append(errorMappers, mapper) }

// registerProtectedMiddleware adds middleware around the authenticated
// routes. The first registered middleware is the outermost.
func registerProtectedMiddleware(middleware protectedMiddleware) {
	protectedMiddlewares = append(protectedMiddlewares, middleware)
}

// extensionKey gives each extension value type its own map key.
type extensionKey[T any] struct{}

// setExtension stores a feature dependency on the gateway, typically from an
// option handler. Extensions are written only while the gateway is built.
func setExtension[T any](g *Gateway, value T) {
	g.extensions[extensionKey[T]{}] = value
}

// extension returns the feature dependency of type T, if one was configured.
func extension[T any](g *Gateway) (T, bool) {
	value, ok := g.extensions[extensionKey[T]{}].(T)
	return value, ok
}

func (g *Gateway) handleExtensionOption(option interface{}) {
	for _, handle := range optionHandlers {
		if handle(g, option) {
			return
		}
	}
}

func (g *Gateway) registerExtensionRoutes(public, protected *http.ServeMux) {
	for _, register := range routeRegistrars {
		register(g, public, protected)
	}
}

func (g *Gateway) wrapProtected(handler http.Handler) http.Handler {
	for index := len(protectedMiddlewares) - 1; index >= 0; index-- {
		handler = protectedMiddlewares[index](g, handler)
	}
	return handler
}

// mapExtensionError reports whether a registered mapper handled err.
func (g *Gateway) mapExtensionError(w http.ResponseWriter, r *http.Request, err error) (httpx.Problem, bool) {
	for _, mapper := range errorMappers {
		if problem, ok := mapper(g, w, r, err); ok {
			return problem, true
		}
	}
	return httpx.Problem{}, false
}
