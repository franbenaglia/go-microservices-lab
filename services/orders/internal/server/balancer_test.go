package server

import (
	"context"
	"testing"

	"github.com/go-kratos/kratos/v3/registry"
	"github.com/go-kratos/kratos/v3/selector"
)

func nodes(weights map[string]string) []selector.Node {
	var out []selector.Node
	for addr, w := range weights {
		md := map[string]string{}
		if w != "" {
			md["weight"] = w
		}
		out = append(out, selector.NewNode("grpc", addr, &registry.ServiceInstance{Name: "inventory-service", Metadata: md}))
	}
	return out
}

func pickN(t *testing.T, n int, ns []selector.Node) map[string]int {
	t.Helper()
	sel := selector.GlobalSelector().Build()
	sel.Apply(ns)
	got := map[string]int{}
	for range n {
		node, done, err := sel.Select(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		done(context.Background(), selector.DoneInfo{})
		got[node.Address()]++
	}
	return got
}

// wrr reparte exactamente por igual sin pesos y respeta el metadato "weight".
func TestWRRWeights(t *testing.T) {
	if err := SetBalancer("wrr"); err != nil {
		t.Fatal(err)
	}
	even := pickN(t, 300, nodes(map[string]string{"a:9000": "", "b:9000": "", "c:9000": ""}))
	for addr, c := range even {
		if c != 100 {
			t.Fatalf("wrr sin pesos: %s recibió %d de 300", addr, c)
		}
	}
	weighted := pickN(t, 500, nodes(map[string]string{"a:9000": "", "b:9000": "300"}))
	if weighted["a:9000"] != 125 || weighted["b:9000"] != 375 {
		t.Fatalf("wrr 100:300 => %v", weighted)
	}
}

// p2c y random reparten entre todos los nodos (sin garantizar igualdad exacta).
func TestOtherBalancersUseAllNodes(t *testing.T) {
	for _, name := range []string{"p2c", "random"} {
		if err := SetBalancer(name); err != nil {
			t.Fatal(err)
		}
		got := pickN(t, 600, nodes(map[string]string{"a:9000": "", "b:9000": "", "c:9000": ""}))
		if len(got) != 3 {
			t.Fatalf("%s no usó los 3 nodos: %v", name, got)
		}
		for addr, c := range got {
			if c < 100 {
				t.Fatalf("%s: %s recibió solo %d de 600", name, addr, c)
			}
		}
	}
	_ = SetBalancer("wrr")
}

func TestSetBalancerInvalid(t *testing.T) {
	if err := SetBalancer("round-robin"); err == nil {
		t.Fatal("debería rechazar un balancer desconocido")
	}
}
