package observability

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// GinMiddleware preserves the original writer (including SSE flushing).
func GinMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := Extract(c.Request.Context(), map[string]string{"traceparent": c.GetHeader("traceparent"), "tracestate": c.GetHeader("tracestate")})
		ctx, end := Operation(ctx, "http")
		c.Request = c.Request.WithContext(ctx)
		if id := TraceID(ctx); id != "" {
			c.Header("X-Trace-ID", id)
		}
		c.Next()
		trace.SpanFromContext(ctx).SetAttributes(attribute.String("http.route", c.FullPath()), attribute.Int("http.response.status_code", c.Writer.Status()))
		var e error
		if c.Writer.Status() >= 400 {
			e = fmt.Errorf("HTTP request failed")
		}
		end(e)
	}
}
