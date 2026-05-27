package server

import (
	"sync"
	"testing"
)

func TestCredSetMatch_SingleCred(t *testing.T) {
	cs := &credSet{}
	cs.set([]Cred{{Username: "alice", Password: "secret"}})
	if !cs.match([]byte("alice"), []byte("secret")) {
		t.Fatal("expected match")
	}
}

func TestCredSetMatch_WrongUser(t *testing.T) {
	cs := &credSet{}
	cs.set([]Cred{{Username: "alice", Password: "secret"}})
	if cs.match([]byte("bob"), []byte("secret")) {
		t.Fatal("should not match wrong user")
	}
}

func TestCredSetMatch_WrongPass(t *testing.T) {
	cs := &credSet{}
	cs.set([]Cred{{Username: "alice", Password: "secret"}})
	if cs.match([]byte("alice"), []byte("wrong")) {
		t.Fatal("should not match wrong password")
	}
}

func TestCredSetMatch_EmptyCreds(t *testing.T) {
	cs := &credSet{}
	if cs.match([]byte("alice"), []byte("secret")) {
		t.Fatal("empty creds should never match")
	}
}

func TestCredSetMatch_MultiCreds(t *testing.T) {
	cs := &credSet{}
	cs.set([]Cred{
		{Username: "alice", Password: "pw1"},
		{Username: "bob", Password: "pw2"},
		{Username: "carol", Password: "pw3"},
	})
	if !cs.match([]byte("bob"), []byte("pw2")) {
		t.Fatal("expected match for bob")
	}
	if cs.match([]byte("alice"), []byte("pw2")) {
		t.Fatal("alice+pw2 should not match")
	}
}

func TestCredSetHasAuth_Empty(t *testing.T) {
	cs := &credSet{}
	if cs.hasAuth() {
		t.Fatal("empty set should have no auth")
	}
}

func TestCredSetHasAuth_NonEmpty(t *testing.T) {
	cs := &credSet{}
	cs.set([]Cred{{Username: "u", Password: "p"}})
	if !cs.hasAuth() {
		t.Fatal("non-empty set should have auth")
	}
}

func TestCredSetConcurrentAccess(t *testing.T) {
	cs := &credSet{}
	cs.set([]Cred{{Username: "user", Password: "pass"}})
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			cs.match([]byte("user"), []byte("pass"))
		}()
		go func() {
			defer wg.Done()
			cs.set([]Cred{{Username: "user", Password: "pass"}})
		}()
	}
	wg.Wait()
}
