package models

import (
	"testing"
	"time"
)

func TestItemTypeValid(t *testing.T) {
	for _, tt := range AllItemTypes {
		if !tt.Valid() {
			t.Errorf("%q should be valid", tt)
		}
	}
	if ItemType("nope").Valid() {
		t.Error("unknown item type should be invalid")
	}
}

func TestItemIsDeleted(t *testing.T) {
	live := Item{}
	if live.IsDeleted() {
		t.Error("zero-value item should not be deleted")
	}
	now := time.Now()
	dead := Item{DeletedAt: &now}
	if !dead.IsDeleted() {
		t.Error("item with DeletedAt set should be deleted")
	}
}
