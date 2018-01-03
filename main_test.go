package main

import (
	"testing"
	"gopkg.in/yaml.v2"
)


func TestMerge(t *testing.T) {
	f1 := []byte(`
---
a:
  b: 43
  c: 32
  4: 11
  5:
    5:
      5: 43
`)
	f2 := []byte(`
a:
  k: 9
  4: 10
  5:
    5:
      6: 14
---
`)

	ret := []byte(`
a:
  b: 43
  c: 32
  4: 11
  5:
    5:
      5: 43
      6: 14
  k: 9
`)

	y1 := make(map[interface{}]interface{})
	err := yaml.Unmarshal(f1, &y1)
	if err != nil {
		t.Fatalf("error :%v", err)
	}

	y2 := make(map[interface{}]interface{})
	err = yaml.Unmarshal(f2, &y2)
	if err != nil {
		t.Fatalf("error :%v", err)
	}

	yret := make(map[interface{}]interface{})
	err = yaml.Unmarshal(ret, &yret)
	if err != nil {
		t.Fatalf("error :%v", err)
	}

	y3 := mergeYAML(y1, y2)
	err = yaml.Unmarshal(f2, &y2)
	if err != nil {
		t.Fatalf("error :%v", err)
	}

	if !compare(yret, y3) {
		t.Fatalf("should equal, got %v and %v", yret, y3)
	}
}

func compare(x1, x2 interface{}) bool {
	switch x1 := x1.(type) {
	case map[string]interface{}:
		x2, ok := x2.(map[string]interface{})
		if !ok {
			return false
		}
		for k, v2 := range x2 {
			if !compare(x1[k], v2) {
				return false
			}
		}
	case map[interface{}]interface{}:
		x2, ok := x2.(map[interface{}]interface{})
		if !ok {
			return false
		}
		for k, v2 := range x2 {
			if !compare(x1[k], v2) {
				return false
			}
		}
	default:
		return x1 == x2
	}
	return true
}
