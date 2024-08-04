package metrics

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

// Regression test: label values taken from fasthttp's reused buffers got
// overwritten by later requests ("GETT", duplicate series) and the whole
// /metrics scrape failed with 500.
func TestFiberMetricsSurviveManyRequests(t *testing.T) {
	app := fiber.New()
	app.Use(Fiber())
	app.Get("/a/:id", func(c *fiber.Ctx) error { return c.SendString("ok") })
	app.Post("/b", func(c *fiber.Ctx) error { return c.SendStatus(201) })

	for i := 0; i < 50; i++ {
		for _, req := range []*struct{ m, p string }{{"GET", "/a/1"}, {"POST", "/b"}, {"DELETE", "/nope"}} {
			if _, err := app.Test(httptest.NewRequest(req.m, req.p, nil)); err != nil {
				t.Fatal(err)
			}
		}
	}

	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	if rec.Code != 200 {
		t.Fatalf("scrape failed (%d): %s", rec.Code, body)
	}
	for _, want := range []string{`method="GET",route="/a/:id",status="200"} 50`, `method="POST",route="/b",status="201"} 50`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("missing %s", want)
		}
	}
}
