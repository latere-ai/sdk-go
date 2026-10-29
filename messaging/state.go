// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package messaging

import (
	"bytes"
	"errors"
	"fmt"
	"slices"

	"latere.ai/x/sdk-go/mls"
)

// MaxPeople bounds how many people a conversation has.
const MaxPeople = 500

// Kind is whether a conversation is between two people or a group.
type Kind uint8

// The kinds of conversation.
const (
	// KindOneOnOne is a conversation between two people. It never takes
	// a third: adding someone starts a group.
	KindOneOnOne Kind = 1
	// KindGroup is a conversation of any number of people.
	KindGroup Kind = 2
)

// String is the kind as the API names it.
func (k Kind) String() string {
	switch k {
	case KindOneOnOne:
		return "one_on_one"
	case KindGroup:
		return "group"
	}
	return fmt.Sprintf("kind(%d)", uint8(k))
}

// MarshalText encodes the kind as the API names it.
func (k Kind) MarshalText() ([]byte, error) {
	if k != KindOneOnOne && k != KindGroup {
		return nil, fmt.Errorf("messaging: unknown kind %d", uint8(k))
	}
	return []byte(k.String()), nil
}

// UnmarshalText decodes a kind the API names.
func (k *Kind) UnmarshalText(b []byte) error {
	for _, v := range []Kind{KindOneOnOne, KindGroup} {
		if string(b) == v.String() {
			*k = v
			return nil
		}
	}
	return fmt.Errorf("messaging: unknown kind %q", b)
}

// Level is how much Latere takes part in a conversation.
type Level uint8

// The levels.
const (
	// LevelOff keeps Latere out: Latere's member is not in the group.
	LevelOff Level = 0
	// LevelReadsAlong lets Latere read and answer whoever asked, and
	// never post to the group.
	LevelReadsAlong Level = 1
	// LevelTakesPart lets Latere post its answers to the group too.
	LevelTakesPart Level = 2
)

// String is the level as the API names it.
func (l Level) String() string {
	switch l {
	case LevelOff:
		return "off"
	case LevelReadsAlong:
		return "reads_along"
	case LevelTakesPart:
		return "takes_part"
	}
	return fmt.Sprintf("level(%d)", uint8(l))
}

// MarshalText encodes the level as the API names it.
func (l Level) MarshalText() ([]byte, error) {
	if l > LevelTakesPart {
		return nil, fmt.Errorf("messaging: unknown level %d", uint8(l))
	}
	return []byte(l.String()), nil
}

// UnmarshalText decodes a level the API names.
func (l *Level) UnmarshalText(b []byte) error {
	for _, v := range []Level{LevelOff, LevelReadsAlong, LevelTakesPart} {
		if string(b) == v.String() {
			*l = v
			return nil
		}
	}
	return fmt.Errorf("messaging: unknown level %q", b)
}

// State is a conversation's state, which every epoch's group context
// carries in the StateExtension, so every member agrees on it or the
// group does not go on.
type State struct {
	Kind Kind
	// Level is how much Latere takes part.
	Level Level
	// People are the members' auth subjects, in the order they joined.
	People []string
	// Owners are the people who decide who joins, who else is an owner
	// and the level, in the order they joined.
	Owners []string
}

// stateVersion is the version of the StateExtension's encoding.
const stateVersion = 1

// Validate reports whether the state is one a group may carry: a known
// kind and level, one to MaxPeople distinct people (two at most in a
// one-on-one), and at least one owner, all of them people, listed in the
// order the people are.
func (s State) Validate() error {
	if s.Kind != KindOneOnOne && s.Kind != KindGroup {
		return fmt.Errorf("messaging: unknown kind %d", uint8(s.Kind))
	}
	if s.Level > LevelTakesPart {
		return fmt.Errorf("messaging: unknown level %d", uint8(s.Level))
	}
	if len(s.People) == 0 || len(s.People) > MaxPeople {
		return fmt.Errorf("messaging: a conversation has 1 to %d people", MaxPeople)
	}
	if s.Kind == KindOneOnOne && len(s.People) > 2 {
		return errors.New("messaging: a one-on-one has two people at most")
	}
	seen := make(map[string]bool, len(s.People))
	for _, p := range s.People {
		if err := validID("person", p); err != nil {
			return err
		}
		if seen[p] {
			return fmt.Errorf("messaging: %s is listed twice", p)
		}
		seen[p] = true
	}
	if len(s.Owners) == 0 {
		return errors.New("messaging: a conversation has an owner")
	}
	last := -1
	for _, o := range s.Owners {
		i := slices.Index(s.People, o)
		if i < 0 {
			return fmt.Errorf("messaging: owner %s is not in the conversation", o)
		}
		if i <= last {
			return errors.New("messaging: owners are listed once each, in the order people joined")
		}
		last = i
	}
	return nil
}

// Has reports whether person is in the conversation.
func (s State) Has(person string) bool { return slices.Contains(s.People, person) }

// Owner reports whether person is an owner.
func (s State) Owner(person string) bool { return slices.Contains(s.Owners, person) }

// Equal reports whether two states are the same.
func (s State) Equal(t State) bool {
	return s.Kind == t.Kind && s.Level == t.Level && slices.Equal(s.People, t.People) && slices.Equal(s.Owners, t.Owners)
}

// Clone returns a copy that shares no slice with s.
func (s State) Clone() State {
	s.People = slices.Clone(s.People)
	s.Owners = slices.Clone(s.Owners)
	return s
}

// Extension encodes the state as the group context extension.
func (s State) Extension() (mls.Extension, error) {
	if err := s.Validate(); err != nil {
		return mls.Extension{}, err
	}
	return s.encode(), nil
}

// encode writes the state, which Validate has bounded.
func (s State) encode() mls.Extension {
	data := encode(func(w *writer) {
		w.u8(stateVersion)
		w.u8(uint8(s.Kind))
		w.u8(uint8(s.Level))
		w.u16(uint16(len(s.People)))
		for _, p := range s.People {
			w.vec8([]byte(p))
		}
		w.u16(uint16(len(s.Owners)))
		for _, o := range s.Owners {
			w.u16(uint16(slices.Index(s.People, o)))
		}
	})
	return mls.Extension{Type: StateExtension, Data: data}
}

// ParseState decodes the state from a group context's extensions, as
// mls.Group.Extensions returns them. It refuses a list without exactly
// one StateExtension, and an encoding Extension would not write.
func ParseState(exts []mls.Extension) (State, error) {
	var data []byte
	n := 0
	for _, e := range exts {
		if e.Type == StateExtension {
			data = e.Data
			n++
		}
	}
	if n != 1 {
		return State{}, errors.New("messaging: the group carries no conversation state")
	}
	return decodeState(data)
}

func decodeState(data []byte) (State, error) {
	r := &reader{b: data}
	var s State
	if v := r.u8(); r.err == nil && v != stateVersion {
		return State{}, fmt.Errorf("messaging: state version %d", v)
	}
	s.Kind = Kind(r.u8())
	s.Level = Level(r.u8())
	n := int(r.u16())
	if r.err == nil && n > MaxPeople {
		r.fail("%d people", n)
	}
	for i := 0; i < n && r.err == nil; i++ {
		s.People = append(s.People, string(r.vec8()))
	}
	m := int(r.u16())
	for i := 0; i < m && r.err == nil; i++ {
		j := int(r.u16())
		if r.err == nil && j >= len(s.People) {
			r.fail("owner %d of %d people", j, len(s.People))
			break
		}
		if r.err == nil {
			s.Owners = append(s.Owners, s.People[j])
		}
	}
	if err := r.done(); err != nil {
		return State{}, err
	}
	if err := s.Validate(); err != nil {
		return State{}, err
	}
	// Validate holds owners to the people's order, so the encoding is the
	// one Extension writes; checking it again costs little and keeps that
	// true if either changes.
	again, err := s.Extension()
	if err != nil {
		return State{}, err
	}
	if !bytes.Equal(again.Data, data) {
		return State{}, errFormat
	}
	return s, nil
}
