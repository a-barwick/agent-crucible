package schema

import "testing"

func TestClassify(t *testing.T) {
	cases := map[string]Kind{
		"search_ticket":    KindRead,
		"lookup_contact":   KindRead,
		"update_ticket":    KindWrite,
		"write_deal":       KindWrite,
		"send_email":       KindEmail,
		"notify_ae":        KindEmail,
		"check_permission": KindPermission,
	}
	for name, want := range cases {
		if got := Classify(name); got != want {
			t.Fatalf("%s: got %s want %s", name, got, want)
		}
	}
}

func TestFind(t *testing.T) {
	tools := []Tool{{Name: "update_ticket"}, {Name: "search_ticket"}}
	got, ok := Find(tools, "update_ticket")
	if !ok || got.Name != "update_ticket" {
		t.Fatalf("%v %v", ok, got)
	}
	if _, ok := Find(tools, "nope"); ok {
		t.Fatal("expected miss")
	}
}
