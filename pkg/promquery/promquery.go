// Package promquery hace consultas instantáneas a la API HTTP de Prometheus
// (/api/v1/query). Lo usan los endpoints de diagnóstico del laboratorio.
package promquery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Client consulta un servidor Prometheus.
type Client struct {
	base string
	http *http.Client
}

// New crea un cliente; base es p. ej. "http://prometheus:9090".
func New(base string) *Client {
	return &Client{base: base, http: &http.Client{Timeout: 3 * time.Second}}
}

type response struct {
	Status string `json:"status"`
	Error  string `json:"error"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  [2]any            `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

// Vector ejecuta una consulta que devuelve un vector y lo indexa por el valor
// de la etiqueta by (p. ej. "job"). Los valores NaN se omiten.
func (c *Client) Vector(ctx context.Context, expr, by string) (map[string]float64, error) {
	u := c.base + "/api/v1/query?query=" + url.QueryEscape(expr)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var r response
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("respuesta de Prometheus: %w", err)
	}
	if r.Status != "success" {
		return nil, fmt.Errorf("prometheus: %s", r.Error)
	}
	out := make(map[string]float64, len(r.Data.Result))
	for _, s := range r.Data.Result {
		str, _ := s.Value[1].(string)
		v, err := strconv.ParseFloat(str, 64)
		if err != nil || v != v { // NaN
			continue
		}
		out[s.Metric[by]] = v
	}
	return out, nil
}
