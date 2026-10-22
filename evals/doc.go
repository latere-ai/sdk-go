// Copyright 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package evals reads Evals, the Latere platform's experiments on agents
// and on programs built on models, whose every number comes with its noise
// and its confounds stated.
//
// An experiment is built from definitions, each kept in versions:
//
//   - a Task is one piece of work for the system under test: its
//     instruction, its inputs, where it starts and the checks it is
//     graded by;
//   - a Metric is a quantity a trial, a cell or a candidate is measured
//     on, such as a pass rate or a cost;
//   - a Grader fills metrics: a check that runs a command, a model or an
//     agent that judges, or a queue a person annotates;
//   - a Suite is a set of tasks with the metrics they are measured by and
//     the one a report leads with;
//   - a Candidate is the system under test: an agent of the platform, a
//     program in an image, or a harness that runs elsewhere and reports.
//
// Applying a definition again with other content makes a new version,
// named by the digest of its content; the newest is its name's current
// version. A reference to a version, a ref, is a name, which reads the
// current version, name@digest, a digest such as "sha256:..." or a
// version's id. Version.Ref gives the name@digest that names one version
// exactly, and an experiment names each definition by its digest alone.
//
// An Experiment runs a suite's tasks for each of its candidates. Each task
// of each candidate is a Cell, which runs its trials; the platform grades
// every trial and computes each Comparison of a candidate with the
// experiment's reference, or of one task with another.
//
//	cfg, err := latere.NewConfig() // the key in LATERE_KEY
//	if err != nil {
//		return err
//	}
//	ev := evals.New(cfg)
//	for e, err := range ev.ListExperiments(ctx, &evals.ListExperimentsParams{State: evals.ExperimentRunning}) {
//		if err != nil {
//			return err
//		}
//		fmt.Println(e.ID, e.State, e.Progress.Trials[evals.TrialDone])
//	}
//
// Client.StreamEvents follows one experiment to its end, connecting again
// after a dropped connection from the last event it delivered.
//
// # Credentials
//
// Evals takes the token every capability of the API takes: a key's,
// exchanged by a Config built with latere.WithKey or from LATERE_KEY, or a
// person's, from latere.NewActorTokens with its default audience. Each call
// acts in the context the token names, a person's own or an
// organization's, and a definition or an experiment of another context
// reads as missing, with an error matching latere.ErrNotFound.
//
// # Scope
//
// This package reads: definitions and their versions, experiments, their
// cells and their comparisons, and an experiment's events as they happen.
// Applying definitions, creating, canceling and closing experiments,
// running trials of one's own, and the figures of a comparison's results
// are not covered yet; call them with latere.Config.Do, and read a
// comparison's figures from its RawJSON.
package evals
