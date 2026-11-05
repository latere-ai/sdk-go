// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package mls_test

import (
	"crypto/rand"
	"fmt"

	"latere.ai/x/sdk-go/mls"
)

// Alice starts a group, adds Bob, and they exchange a message.
func Example() {
	const suite = mls.MLS_128_DHKEMX25519_AES128GCM_SHA256_Ed25519
	cfg := mls.Config{}

	// Bob's device publishes a key package and keeps its secrets.
	bobKey, err := mls.GenerateSignatureKey(suite)
	if err != nil {
		panic(err)
	}
	bobKP, bobSecrets, err := mls.NewKeyPackage(bobKey, mls.BasicCredential([]byte("bob")), mls.KeyPackageOptions{})
	if err != nil {
		panic(err)
	}

	// Alice's device creates the group and adds Bob.
	aliceKey, err := mls.GenerateSignatureKey(suite)
	if err != nil {
		panic(err)
	}
	groupID := make([]byte, 32)
	if _, err := rand.Read(groupID); err != nil {
		panic(err)
	}
	alice, err := mls.CreateGroup(groupID, aliceKey, mls.BasicCredential([]byte("alice")), mls.CreateOptions{}, cfg)
	if err != nil {
		panic(err)
	}
	commit, err := alice.Commit(mls.CommitOptions{Add: []*mls.KeyPackage{bobKP}})
	if err != nil {
		panic(err)
	}
	// Once the delivery service accepts commit.Message():
	if err := alice.Merge(commit); err != nil {
		panic(err)
	}

	// Bob joins with the Welcome.
	bob, err := mls.JoinGroup(commit.Welcome(), bobKP, bobSecrets, bobKey, mls.JoinOptions{}, cfg)
	if err != nil {
		panic(err)
	}

	msg, err := alice.Encrypt([]byte("hello, Bob"), nil)
	if err != nil {
		panic(err)
	}
	got, err := bob.Process(msg)
	if err != nil {
		panic(err)
	}
	fmt.Printf("%s says: %s\n", got.Sender.Credential.Identity, got.ApplicationData)
	// Output: alice says: hello, Bob
}
