// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package messaging

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"fmt"
	"slices"

	"latere.ai/x/sdk-go/mls"
)

// Every device checks each commit before it merges it, and the service
// checks the same before it accepts one, with Policy.CheckCommit: who
// proposed each change, against the conversation's state of the epoch
// the commit leaves.
//
//   - An owner adds a person, with all their devices, removes another
//     person, makes or unmakes an owner, sets the level, and adds or
//     removes Latere's member.
//   - A person adds and removes their own devices, and leaves by
//     proposing the removal of their own leaves; any member's commit
//     carries such a proposal.
//   - A person's new device joins on its own by an external commit,
//     which removes no one.
//   - The service proposes removals only, of devices whose keys their
//     person revoked.
//   - Latere's member commits an update of its own leaf and nothing
//     else.
//   - A one-on-one takes its second person in the commit that starts its
//     first epoch, and never a third.
//
// After the commit every leaf is a device of a listed person or Latere's
// member; every listed person has a device; Latere's member is in the
// group exactly when the level is not Off; and the owners are people of
// the conversation. When the last owner leaves, the person who joined
// first becomes the owner, written by the same commit.

// ErrRefused is the error for a commit, proposal or message the rules
// refuse. The errors CheckCommit, CheckProposal and CheckMessage return
// for a rule wrap it.
var ErrRefused = errors.New("messaging: refused by the conversation's rules")

func refuse(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrRefused, fmt.Sprintf(format, args...))
}

// Policy is what commits are checked against.
type Policy struct {
	// Installation is the installation's Latere key, which Latere's
	// member's attestation verifies under. The apps pin it.
	Installation ed25519.PublicKey
	// Directory returns the verified directory of a person, as
	// NewDirectory makes it from the records the service lists.
	Directory func(person string) (*Directory, error)
}

// Removal is a person a commit removes, and who proposed it.
type Removal struct {
	Person string
	// By is the member who proposed it; zero when the service did.
	By Identity
	// ByService is set when the service proposed the removal of a
	// device the person revoked, of each of their devices that left.
	ByService bool
}

// Change is what a commit does to a conversation, in its own terms, for
// the line every member's device draws from the commit itself.
type Change struct {
	// By is the committer.
	By Identity
	// Before and After are the state of the epoch the commit leaves and
	// of the one it starts. Owners and the level changed if they differ.
	Before, After State
	// Joined are the people the commit adds, in the order they joined.
	Joined []string
	// Left are the people it removes who proposed it themselves.
	Left []string
	// Removed are the people it removes on someone else's proposal.
	Removed []Removal
	// DevicesAdded and DevicesRemoved are devices of people who stay.
	DevicesAdded, DevicesRemoved []Identity
	// ExternalJoin is set when a person's new device joined on its own;
	// it is the one device in DevicesAdded.
	ExternalJoin bool
	// LatereAdded and LatereRemoved say whether Latere's member joined
	// or left.
	LatereAdded, LatereRemoved bool
}

// leaf is a leaf of the group with whose it is.
type leaf struct {
	member mls.Member
	id     Identity
}

// Group is the epoch a commit or proposal comes to: an mls.Group, or the
// service's mls.PublicGroup, before the commit is merged.
type Group interface {
	GroupID() []byte
	Extensions() []mls.Extension
	Members() []mls.Member
}

// CheckCommit checks a staged commit against the rules, with the group
// at the epoch the commit leaves. It returns what the commit does, or an
// error wrapping ErrRefused for a rule it breaks.
func (p *Policy) CheckCommit(g Group, c mls.Changes) (*Change, error) {
	extensions := g.Extensions()
	before, err := ParseState(extensions)
	if err != nil {
		return nil, err
	}
	after := before
	if c.ExtensionsChanged {
		if after, err = ParseState(c.Extensions); err != nil {
			return nil, refuse("the commit's state: %v", err)
		}
		if !sameOtherExtensions(extensions, c.Extensions) {
			return nil, refuse("a commit changes no extension but the conversation's state")
		}
	}
	if after.Kind != before.Kind {
		return nil, refuse("a conversation's kind does not change")
	}
	members := g.Members()
	leaves := make(map[uint32]leaf, len(members))
	for _, m := range members {
		id, err := ParseCredential(m.Credential)
		if err != nil {
			return nil, err
		}
		leaves[m.Leaf] = leaf{m, id}
	}
	ch := &Change{Before: before.Clone(), After: after.Clone()}
	if c.ExternalJoin {
		return ch, p.checkExternalJoin(g.GroupID(), before, c, ch)
	}
	if c.Sender.Kind != mls.SenderMember {
		return nil, refuse("a commit comes from a member or a joining device")
	}
	by, ok := leaves[c.Sender.Leaf]
	if !ok {
		return nil, refuse("the committer is not in the group")
	}
	ch.By = by.id
	if by.id.Latere() && (len(c.Proposals) > 0 || len(c.Added) > 0 || len(c.Removed) > 0 || c.ExtensionsChanged) {
		return nil, refuse("Latere's member commits an update of its own leaf alone")
	}

	// Who proposed what. A commit's own proposals are the committer's.
	var stateBy *Identity
	removed := make(map[uint32]Identity) // leaf to who proposed it, zero for the service
	var removedByService []uint32
	addedBy := make([]Identity, 0, len(c.Added))
	for _, pr := range c.Proposals {
		var proposer Identity
		external := false
		switch pr.Sender.Kind {
		case mls.SenderMember:
			l, ok := leaves[pr.Sender.Leaf]
			if !ok {
				return nil, refuse("a proposal comes from a leaf not in the group")
			}
			proposer = l.id
		case mls.SenderExternal:
			external = true
		default:
			return nil, refuse("a proposal comes from a member or the service")
		}
		if external && pr.Kind != mls.ProposalRemove {
			return nil, refuse("the service proposes removals alone")
		}
		if !external && proposer.Latere() {
			return nil, refuse("Latere's member proposes nothing")
		}
		switch pr.Kind {
		case mls.ProposalAdd:
			addedBy = append(addedBy, proposer)
		case mls.ProposalRemove:
			target, ok := leaves[pr.Leaf]
			if !ok {
				return nil, refuse("a removal is of a leaf not in the group")
			}
			switch {
			case external:
				if err := p.checkServiceRemoval(target); err != nil {
					return nil, err
				}
				removedByService = append(removedByService, pr.Leaf)
			case target.id.Latere(), target.id.Person != proposer.Person:
				if !before.Owner(proposer.Person) {
					return nil, refuse("only an owner removes another person or Latere's member")
				}
			}
			removed[pr.Leaf] = proposer
		case mls.ProposalUpdate:
			// MLS lets a leaf propose only its own update; the updated
			// leaf is checked with the others below.
		case mls.ProposalGroupContextExtensions:
			who := proposer
			stateBy = &who
		default:
			return nil, refuse("a conversation takes no %v proposal", pr.Kind)
		}
	}
	if len(addedBy) != len(c.Added) || len(removed) != len(c.Removed) {
		return nil, refuse("the commit's proposals and changes disagree")
	}

	// The leaves after the commit.
	groupID := g.GroupID()
	next := make(map[uint32]leaf, len(leaves))
	for i, l := range leaves {
		next[i] = l
	}
	for _, m := range c.Removed {
		delete(next, m.Leaf)
	}
	for _, m := range c.Updated {
		id, err := ParseCredential(m.Credential)
		if err != nil {
			return nil, err
		}
		old, ok := leaves[m.Leaf]
		if !ok || old.id != id {
			return nil, refuse("an update keeps the leaf's identity")
		}
		if err := p.checkLeaf(groupID, id, m); err != nil {
			return nil, err
		}
		next[m.Leaf] = leaf{m, id}
	}
	var newPeople []string
	for i, m := range c.Added {
		id, err := ParseCredential(m.Credential)
		if err != nil {
			return nil, err
		}
		proposer := addedBy[i]
		switch {
		case id.Latere():
			if !before.Owner(proposer.Person) {
				return nil, refuse("only an owner lets Latere in")
			}
			ch.LatereAdded = true
		case before.Has(id.Person):
			if proposer.Person != id.Person {
				return nil, refuse("a person adds their own devices")
			}
			ch.DevicesAdded = append(ch.DevicesAdded, id)
		default:
			if !before.Owner(proposer.Person) {
				return nil, refuse("only an owner adds a person")
			}
			if before.Kind == KindOneOnOne && c.Epoch != 1 {
				return nil, refuse("a one-on-one takes its second person when it starts, and never a third")
			}
			if !slices.Contains(newPeople, id.Person) {
				newPeople = append(newPeople, id.Person)
			}
		}
		if err := p.checkLeaf(groupID, id, m); err != nil {
			return nil, err
		}
		next[m.Leaf] = leaf{m, id}
	}

	// Who is left, and whether the state says so.
	has := make(map[string]bool)
	latere := 0
	for _, l := range next {
		if l.id.Latere() {
			latere++
		} else {
			has[l.id.Person] = true
		}
	}
	var people []string
	for _, person := range before.People {
		if has[person] {
			people = append(people, person)
		}
	}
	people = append(people, newPeople...)
	if len(people) != len(has) {
		return nil, refuse("every leaf is a device of a person of the conversation")
	}
	if !slices.Equal(people, after.People) {
		return nil, refuse("the commit's state lists the people it leaves in the group")
	}
	ch.Joined = newPeople
	// A person whose every leaf goes left when they proposed each
	// removal themselves; otherwise someone removed them: a member when
	// any member did, the service when only the service and they did.
	type gone struct {
		byService bool
		by        *Identity
	}
	goneBy := make(map[string]*gone)
	var goneOrder []string
	for _, m := range c.Removed {
		l := leaves[m.Leaf]
		if l.id.Latere() {
			ch.LatereRemoved = true
			continue
		}
		if has[l.id.Person] {
			ch.DevicesRemoved = append(ch.DevicesRemoved, l.id)
			continue
		}
		g, seen := goneBy[l.id.Person]
		if !seen {
			g = &gone{}
			goneBy[l.id.Person] = g
			goneOrder = append(goneOrder, l.id.Person)
		}
		by := removed[m.Leaf]
		switch {
		case slices.Contains(removedByService, m.Leaf):
			g.byService = true
		case by.Person != l.id.Person && g.by == nil:
			who := by
			g.by = &who
		}
	}
	for _, person := range goneOrder {
		switch g := goneBy[person]; {
		case g.by != nil:
			ch.Removed = append(ch.Removed, Removal{Person: person, By: *g.by})
		case g.byService:
			ch.Removed = append(ch.Removed, Removal{Person: person, ByService: true})
		default:
			ch.Left = append(ch.Left, person)
		}
	}

	// Owners: those who stay, unless an owner changed them, or the
	// first person when the last owner left.
	var kept []string
	for _, o := range before.Owners {
		if has[o] {
			kept = append(kept, o)
		}
	}
	if !slices.Equal(kept, after.Owners) {
		switch {
		case len(kept) == 0:
			if !slices.Equal(after.Owners, people[:1]) {
				return nil, refuse("when the last owner leaves, the person who joined first is the owner")
			}
		case stateBy == nil || !before.Owner(stateBy.Person) || !has[stateBy.Person]:
			return nil, refuse("only an owner changes the owners")
		}
	}
	if after.Level != before.Level && (stateBy == nil || !before.Owner(stateBy.Person) || !has[stateBy.Person]) {
		return nil, refuse("only an owner sets the level")
	}
	if latere > 1 {
		return nil, refuse("a conversation has one Latere member")
	}
	if (latere == 1) != (after.Level != LevelOff) {
		return nil, refuse("Latere's member is in the group exactly when the level is not Off")
	}

	// A commit that lets a person or Latere in tells them what history
	// they get.
	if len(newPeople) > 0 || ch.LatereAdded {
		n, err := ParseCommitNotice(c.AuthenticatedData)
		if err != nil {
			return nil, refuse("a commit that lets someone in carries its history notice: %v", err)
		}
		for _, m := range c.Added {
			id, _ := ParseCredential(m.Credential) // parsed above
			if (id.Latere() || slices.Contains(newPeople, id.Person)) && !n.Covers(m.SignatureKey) {
				return nil, refuse("the history notice has no part for a leaf the commit adds")
			}
		}
		if mg, ok := g.(*mls.Group); ok {
			if _, err := n.Range(mg); err != nil {
				return nil, refuse("the history notice's part for the members does not open: %v", err)
			}
		}
	}
	return ch, nil
}

// CheckJoin checks a group before a device takes it for the
// conversation, once it joined by a Welcome or on its own, and before
// the service takes the group a conversation starts with: every leaf is
// a device of a person of the conversation that stands in their
// directory, or Latere's member attested for the group; every person
// has a device; the state holds; and Latere's member is in the group
// exactly when the level is not Off. A group the service made up fails
// it, since the leaves of a person's are theirs to sign.
func (p *Policy) CheckJoin(g Group) error {
	s, err := ParseState(g.Extensions())
	if err != nil {
		return refuse("the group's state: %v", err)
	}
	if err := s.Validate(); err != nil {
		return refuse("the group's state: %v", err)
	}
	has := make(map[string]bool, len(s.People))
	latere := 0
	groupID := g.GroupID()
	for _, m := range g.Members() {
		id, err := ParseCredential(m.Credential)
		if err != nil {
			return refuse("a leaf's credential: %v", err)
		}
		if !id.Latere() && !s.Has(id.Person) {
			return refuse("every leaf is a device of a person of the conversation")
		}
		if err := p.checkLeaf(groupID, id, m); err != nil {
			return err
		}
		if id.Latere() {
			latere++
		} else {
			has[id.Person] = true
		}
	}
	if len(has) != len(s.People) {
		return refuse("every person of the conversation has a device in the group")
	}
	if latere > 1 {
		return refuse("a conversation has one Latere member")
	}
	if (latere == 1) != (s.Level != LevelOff) {
		return refuse("Latere's member is in the group exactly when the level is not Off")
	}
	return nil
}

// JoinGroup joins a conversation's group by a Welcome with the policy's
// configuration and takes the group only once CheckJoin passes and a
// person's device signed the Welcome's group info. Latere's member adds
// no one, so a Welcome it signed is of a group it made, which it never
// may, whatever the group's leaves and level say.
func (p *Policy) JoinGroup(welcome []byte, kp *mls.KeyPackage, secrets *mls.KeyPackageSecrets, signer *mls.SignatureKey, opts mls.JoinOptions) (*mls.Group, error) {
	g, err := mls.JoinGroup(welcome, kp, secrets, signer, opts, p.Config())
	if err != nil {
		return nil, err
	}
	if err := p.checkJoined(g); err != nil {
		return nil, err
	}
	return g, nil
}

// JoinExternal joins a conversation's group on its own, from a group
// info the service hands out, with the policy's configuration, and
// returns the group and the commit to send only once CheckJoin passes and
// a person's device signed the group info. A group info Latere's member
// signed after an update of its own is refused with the rest, since a
// group Latere's member made has no other kind; until a person commits,
// the device joins by a Welcome from another of its person's devices.
//
// minEpoch is the conversation's epoch as the device approving this one
// knew it, handed over the pairing with the conversation: a group info
// of an earlier epoch is refused, since a member removed since then,
// Latere's among them, may still hold that epoch's secrets and read
// what the device sends. Zero takes a group info of any epoch.
func (p *Policy) JoinExternal(groupInfo []byte, signer *mls.SignatureKey, id Identity, minEpoch uint64) (*mls.Group, []byte, error) {
	info, err := mls.PeekMessage(groupInfo)
	if err != nil {
		return nil, nil, err
	}
	if info.Epoch < minEpoch {
		return nil, nil, refuse("the group info is of epoch %d, before the conversation's epoch %d the device was given", info.Epoch, minEpoch)
	}
	g, msg, err := mls.JoinExternal(groupInfo, signer, id.Credential(), mls.ExternalJoinOptions{SupportedExtensions: slices.Clone(extensionTypes)}, p.Config())
	if err != nil {
		return nil, nil, err
	}
	if err := p.checkJoined(g); err != nil {
		return nil, nil, err
	}
	return g, msg, nil
}

// checkJoined checks a group a device just joined: CheckJoin, and that
// the group info it joined from is a person's device's, not Latere's
// member's.
func (p *Policy) checkJoined(g *mls.Group) error {
	m, ok := g.GroupInfoSigner()
	if !ok {
		return refuse("the group names no member that signed its group info")
	}
	id, err := ParseCredential(m.Credential)
	if err != nil {
		return refuse("the group info's signer: %v", err)
	}
	if id.Latere() {
		return refuse("Latere's member signed the group info: a group it made, or one a person's device has not committed to since its update")
	}
	return p.CheckJoin(g)
}

// checkExternalJoin checks a commit by a person's new device joining on
// its own: a device of a person of the conversation, vouched for, that
// changes nothing else.
func (p *Policy) checkExternalJoin(groupID []byte, before State, c mls.Changes, ch *Change) error {
	if c.Sender.Kind != mls.SenderNewMember {
		return refuse("a commit comes from a member or a joining device")
	}
	if len(c.Added) != 1 || len(c.Removed) != 0 || c.ExtensionsChanged {
		return refuse("a device joining on its own changes nothing else")
	}
	for _, pr := range c.Proposals {
		if pr.Kind != mls.ProposalExternalInit {
			return refuse("a device joining on its own makes no %v proposal", pr.Kind)
		}
	}
	m := c.Added[0]
	id, err := ParseCredential(m.Credential)
	if err != nil {
		return err
	}
	if id.Latere() || !before.Has(id.Person) {
		return refuse("a device joins on its own only for a person of the conversation")
	}
	if err := p.checkLeaf(groupID, id, m); err != nil {
		return err
	}
	ch.By = id
	ch.ExternalJoin = true
	ch.DevicesAdded = []Identity{id}
	return nil
}

// checkLeaf checks a leaf the commit adds or updates: a device of its
// person in the directory, or Latere's member attested for the group,
// in the suite of conversations.
func (p *Policy) checkLeaf(groupID []byte, id Identity, m mls.Member) error {
	if !slices.Contains(m.Capabilities.CipherSuites, Suite) {
		return refuse("a leaf supports the suite of conversations")
	}
	if id.Latere() {
		if err := CheckLatere(p.Installation, groupID, m); err != nil {
			return fmt.Errorf("%w: %v", ErrRefused, err)
		}
		return nil
	}
	if p.Directory == nil {
		return errors.New("messaging: the policy has no directory")
	}
	d, err := p.Directory(id.Person)
	if err != nil {
		return err
	}
	if err := d.CheckMember(m); err != nil {
		return fmt.Errorf("%w: %v", ErrRefused, err)
	}
	return nil
}

// sameOtherExtensions reports whether two lists hold the same extensions
// apart from the conversation's state.
func sameOtherExtensions(a, b []mls.Extension) bool {
	other := func(exts []mls.Extension) []mls.Extension {
		var out []mls.Extension
		for _, e := range exts {
			if e.Type != StateExtension {
				out = append(out, e)
			}
		}
		return out
	}
	return slices.EqualFunc(other(a), other(b), func(x, y mls.Extension) bool {
		return x.Type == y.Type && bytes.Equal(x.Data, y.Data)
	})
}

// CheckProposal checks a proposal as it arrives, before any commit
// carries it, so a device discards one the rules refuse whoever commits
// it, as Policy.Config has every group do: the service proposes the
// removal of a revoked device alone; Latere's member proposes nothing;
// an owner proposes adding a person or Latere's member, removing another
// person or Latere's member, and a change of the state; a person
// proposes adding and removing their own devices; every leaf proposed is
// one the directory or the installation vouches for; and a conversation
// takes no pre-shared key, reinitialization or proposal from a joining
// client.
func (p *Policy) CheckProposal(g Group, pr mls.Proposal) error { return checkProposal(g, pr, p) }

// checkProposal is CheckProposal; with no policy it decides what the
// conversation's state decides alone, which Config has every group
// check.
func checkProposal(g Group, pr mls.Proposal, p *Policy) error {
	st, err := ParseState(g.Extensions())
	if err != nil {
		return err
	}
	target := func(leafIndex uint32) (leaf, error) {
		for _, m := range g.Members() {
			if m.Leaf == leafIndex {
				id, err := ParseCredential(m.Credential)
				if err != nil {
					return leaf{}, err
				}
				return leaf{m, id}, nil
			}
		}
		return leaf{}, refuse("a removal is of a leaf not in the group")
	}
	var proposer Identity
	switch pr.Sender.Kind {
	case mls.SenderExternal:
		if pr.Kind != mls.ProposalRemove {
			return refuse("the service proposes removals alone")
		}
		t, err := target(pr.Leaf)
		if err != nil {
			return err
		}
		if p == nil {
			if t.id.Latere() {
				return refuse("an owner removes Latere's member, not the service")
			}
			return nil
		}
		return p.checkServiceRemoval(t)
	case mls.SenderMember:
		if proposer, err = ParseCredential(pr.Sender.Credential); err != nil {
			return err
		}
		if proposer.Latere() {
			return refuse("Latere's member proposes nothing")
		}
	default:
		return refuse("a proposal comes from a member or the service")
	}
	owner := st.Owner(proposer.Person)
	switch pr.Kind {
	case mls.ProposalAdd:
		if pr.Member == nil {
			return refuse("an add names the device it adds")
		}
		id, err := ParseCredential(pr.Member.Credential)
		if err != nil {
			return err
		}
		switch {
		case id.Latere() && !owner:
			return refuse("only an owner lets Latere in")
		case !id.Latere() && st.Has(id.Person) && id.Person != proposer.Person:
			return refuse("a person adds their own devices")
		case !id.Latere() && !st.Has(id.Person) && !owner:
			return refuse("only an owner adds a person")
		}
		if p != nil {
			return p.checkLeaf(g.GroupID(), id, *pr.Member)
		}
	case mls.ProposalRemove:
		t, err := target(pr.Leaf)
		if err != nil {
			return err
		}
		if (t.id.Latere() || t.id.Person != proposer.Person) && !owner {
			return refuse("only an owner removes another person or Latere's member")
		}
	case mls.ProposalUpdate:
		if p != nil && pr.Member != nil {
			id, err := ParseCredential(pr.Member.Credential)
			if err != nil {
				return err
			}
			if id != proposer {
				return refuse("an update keeps the leaf's identity")
			}
			return p.checkLeaf(g.GroupID(), id, *pr.Member)
		}
	case mls.ProposalGroupContextExtensions:
		if !owner {
			return refuse("only an owner proposes a change of the conversation's state")
		}
		next, err := ParseState(pr.Extensions)
		if err != nil {
			return refuse("the proposal's state: %v", err)
		}
		if next.Kind != st.Kind || !sameOtherExtensions(g.Extensions(), pr.Extensions) {
			return refuse("a proposal changes no extension but the conversation's state")
		}
	default:
		return refuse("a conversation takes no %v proposal", pr.Kind)
	}
	return nil
}

// checkServiceRemoval checks a removal the service proposes: of a device
// of a person, never Latere's member, whose key the person revoked.
func (p *Policy) checkServiceRemoval(t leaf) error {
	if t.id.Latere() {
		return refuse("an owner removes Latere's member, not the service")
	}
	if p.Directory == nil {
		return errors.New("messaging: the policy has no directory")
	}
	d, err := p.Directory(t.id.Person)
	if err != nil {
		return err
	}
	if !d.Revoked(t.member.SignatureKey) {
		return refuse("the service removes a device only once its person revoked it")
	}
	return nil
}

// CheckMessage checks an application message's sender against the
// state: Latere's member posts to the group only under Takes part.
func CheckMessage(s State, sender mls.Sender) error {
	if sender.Kind != mls.SenderMember {
		return refuse("a message comes from a member")
	}
	id, err := ParseCredential(sender.Credential)
	if err != nil {
		return err
	}
	if id.Latere() && s.Level != LevelTakesPart {
		return refuse("Latere's member posts to the group only when Latere takes part")
	}
	return nil
}
