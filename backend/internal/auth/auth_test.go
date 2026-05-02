package auth

import (
	"testing"
	"context"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

func TestHashToken_deterministic(t *testing.T) {
	h1 := hashToken("mytoken")
	h2 := hashToken("mytoken")
	if h1 != h2 {
		t.Error("hashToken must be deterministic")
	}
	if h1 == "" {
		t.Error("hash must not be empty")
	}
}

func TestHashToken_distinct(t *testing.T) {
	if hashToken("token_a") == hashToken("token_b") {
		t.Error("different tokens must have different hashes")
	}
}

func TestGenerateToken_unique(t *testing.T) {
	t1, err := generateToken()
	if err != nil {
		t.Fatal(err)
	}
	t2, err := generateToken()
	if err != nil {
		t.Fatal(err)
	}
	if t1 == t2 {
		t.Error("generateToken must produce unique tokens")
	}
	if len(t1) < 40 {
		t.Errorf("token too short: %q", t1)
	}
}

func TestSessionCache_getSet(t *testing.T) {
	c := newSessionCache(core.SystemClock{})
	hash := hashToken("tok123")
	p := Principal{
		UserID: core.NewUserID(),
		UserKind: UserKindSeller,
	}
	c.set(hash, p)

	got, ok := c.get(hash)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.UserID != p.UserID {
		t.Errorf("wrong principal: got %s want %s", got.UserID, p.UserID)
	}
}

func TestSessionCache_miss(t *testing.T) {
	c := newSessionCache(core.SystemClock{})
	_, ok := c.get("nonexistent_hash")
	if ok {
		t.Error("expected miss for unknown hash")
	}
}

func TestSessionCache_delete(t *testing.T) {
	c := newSessionCache(core.SystemClock{})
	hash := hashToken("tok456")
	c.set(hash, Principal{UserID: core.NewUserID()})
	c.delete(hash)
	_, ok := c.get(hash)
	if ok {
		t.Error("expected miss after delete")
	}
}

func TestSessionCache_lruEviction(t *testing.T) {
	c := newSessionCache(core.SystemClock{})
	c.cap = 3 // tiny capacity

	// Fill to capacity.
	hashes := make([]string, 4)
	for i := range hashes {
		hashes[i] = hashToken("tok" + string(rune('A'+i)))
		c.set(hashes[i], Principal{UserID: core.NewUserID()})
	}
	// First entry should have been evicted.
	_, ok := c.get(hashes[0])
	if ok {
		t.Error("oldest entry should be evicted when cap exceeded")
	}
	// Most recent should still be there.
	_, ok = c.get(hashes[3])
	if !ok {
		t.Error("newest entry should remain after eviction")
	}
}

func TestWithPrincipal_roundtrip(t *testing.T) {
	p := Principal{
		UserID:   core.NewUserID(),
		UserKind: UserKindPikshippAdmin,
		AuthMethod: "session",
	}
	ctx := WithPrincipal(context.Background(), p)
	got, ok := PrincipalFrom(ctx)
	if !ok {
		t.Fatal("expected principal in context")
	}
	if got.UserID != p.UserID {
		t.Errorf("UserID mismatch: got %s want %s", got.UserID, p.UserID)
	}
	if got.UserKind != p.UserKind {
		t.Errorf("UserKind mismatch: got %s want %s", got.UserKind, p.UserKind)
	}
}

func TestPrincipalFrom_absent(t *testing.T) {
	_, ok := PrincipalFrom(context.Background())
	if ok {
		t.Error("expected false for context without principal")
	}
}

func TestMustPrincipalFrom_panicsWhenAbsent(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic when principal absent")
		}
	}()
	MustPrincipalFrom(context.Background())
}
