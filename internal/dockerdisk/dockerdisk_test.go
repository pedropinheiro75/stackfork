package dockerdisk

import (
	"reflect"
	"testing"
)

func TestParseDF(t *testing.T) {
	u, err := parseDF("Filesystem 1024-blocks Used Available Capacity Mounted on\noverlay 62062592 46143640 15918952 74% /\n")
	if err != nil || u.Percent != 74 || u.AvailKB != 15918952 || u.Full() {
		t.Fatalf("got %+v %v", u, err)
	}
	if u, _ := parseDF("overlay 1 1 0 91% /"); !u.Full() {
		t.Fatal("91% should be full")
	}
	if _, err := parseDF("garbage"); err == nil {
		t.Fatal("want error")
	}
}

func TestAnonymousVolumesNeverNamed(t *testing.T) {
	anon := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	got := AnonymousVolumes([]string{"app_postgres_data", anon, "idv-x_pgdata", "ABC"})
	if !reflect.DeepEqual(got, []string{anon}) {
		t.Fatalf("got %v", got)
	}
}
