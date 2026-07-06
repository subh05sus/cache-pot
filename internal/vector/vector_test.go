package vector

import "testing"

func TestSetSearch(t *testing.T) {
	c := NewCollection()
	if err := c.Set("a", []float32{1, 0, 0}, "alpha"); err != nil {
		t.Fatal(err)
	}
	c.Set("b", []float32{0, 1, 0}, "beta")
	c.Set("c", []float32{0.9, 0.1, 0}, "gamma")

	res, err := c.Search([]float32{1, 0, 0}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("want 2 results, got %d", len(res))
	}
	if res[0].Item.ID != "a" {
		t.Fatalf("nearest should be a, got %s", res[0].Item.ID)
	}
	if res[0].Score < 0.99 {
		t.Fatalf("identical vector should score ~1, got %f", res[0].Score)
	}
}

func TestDimMismatch(t *testing.T) {
	c := NewCollection()
	c.Set("a", []float32{1, 2, 3}, "")
	if err := c.Set("b", []float32{1, 2}, ""); err != ErrDimMismatch {
		t.Fatalf("want ErrDimMismatch, got %v", err)
	}
}
