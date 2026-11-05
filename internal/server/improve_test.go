package server

import (
	"strings"
	"testing"

	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

func TestTidyBook(t *testing.T) {
	cases := []struct {
		title   string
		authors []string
		index   float64
		title2  string
		want    string
		index2  float64
	}{
		{"1. Izabrana", []string{"Ally Condie"}, 0, "Izabrana", "Ally Condie", 1},
		{"10. Andjelika i urota tame", []string{"Anne i Serge Golon"}, 0, "Andjelika i urota tame", "Anne i Serge Golon", 10},
		{"1984", []string{"George Orwell"}, 0, "1984", "George Orwell", 0},
		{"2010: Druga odiseja", []string{"Arthur C. Clarke"}, 0, "2010: Druga odiseja", "Arthur C. Clarke", 0},
		{"DO VIÐENJA I HVALA NA SVIM RIBAMA", []string{"Adams Daglas"}, 0, "Do viðenja i hvala na svim ribama", "Adams Daglas", 0},
		{"Barbara Frale - Templari", []string{"Barbara Frale"}, 0, "Templari", "Barbara Frale", 0},
		{"Zbogom", []string{"Kulin, Ayse"}, 0, "Zbogom", "Ayse Kulin", 0},
		{"Postaja Na Obzorju", []string{"ERIH MARIA REMARK"}, 0, "Postaja Na Obzorju", "Erih Maria Remark", 0},
		{"Rat i mir II", []string{"Lav Tolstoj"}, 0, "Rat i mir II", "Lav Tolstoj", 0},
	}
	for _, c := range cases {
		b := &store.Book{ID: 1, Title: c.title, SeriesIndex: c.index, Locked: []string{}}
		for _, a := range c.authors {
			b.Authors = append(b.Authors, store.AuthorRef{Name: a})
		}
		m, _ := tidyBook(b)
		if m.Title != c.title2 || strings.Join(m.Authors, ", ") != c.want || m.SeriesIndex != c.index2 {
			t.Errorf("%q by %v → %q by %q #%v", c.title, c.authors, m.Title, strings.Join(m.Authors, ", "), m.SeriesIndex)
		}
	}
	// a locked title stays as it is
	b := &store.Book{ID: 1, Title: "1. Izabrana", Locked: []string{"title"}, Authors: []store.AuthorRef{{Name: "Ally Condie"}}}
	if m, ch := tidyBook(b); m.Title != "1. Izabrana" || len(ch) != 0 {
		t.Errorf("locked: %q %v", m.Title, ch)
	}
}
