/*
 * This file is part of the kiagnose project
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * Copyright 2023 Red Hat, Inc.
 *
 */

package launcher_test

import (
	"context"
	"errors"
	"testing"

	assert "github.com/stretchr/testify/require"

	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/config"
	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/launcher"
	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/status"
)

var (
	errReport   = errors.New("report error")
	errSetup    = errors.New("setup error")
	errRun      = errors.New("run error")
	errTeardown = errors.New("teardown error")
)

func TestLauncherRunShouldSucceed(t *testing.T) {
	testLauncher := launcher.New(checkupStub{}, &reporterStub{})
	assert.NoError(t, testLauncher.Run(context.Background()))
}

func TestLauncherRunShouldFailWhen(t *testing.T) {
	tests := map[string]struct {
		checkup  checkupStub
		reporter reporterStub
		errors   []string
	}{
		"report fails": {checkup: checkupStub{},
			reporter: reporterStub{failReport: errReport},
			errors:   []string{errReport.Error()}},
		"setup fails": {checkup: checkupStub{failSetup: errSetup},
			reporter: reporterStub{},
			errors:   []string{errSetup.Error()}},
		"run fails": {checkup: checkupStub{failRun: errRun},
			reporter: reporterStub{},
			errors:   []string{errRun.Error()}},
		"teardown fails": {checkup: checkupStub{failTeardown: errTeardown},
			reporter: reporterStub{},
			errors:   []string{errTeardown.Error()}},
		"setup and 2nd report fail": {checkup: checkupStub{failSetup: errSetup},
			reporter: reporterStub{failReport: errReport, failOnSecondReport: true},
			errors:   []string{errSetup.Error(), errReport.Error()}},
		"run and report fail": {checkup: checkupStub{failRun: errRun},
			reporter: reporterStub{failReport: errReport, failOnSecondReport: true},
			errors:   []string{errRun.Error(), errReport.Error()}},
		"teardown and report fail": {checkup: checkupStub{failTeardown: errTeardown},
			reporter: reporterStub{failReport: errReport, failOnSecondReport: true},
			errors:   []string{errTeardown.Error(), errReport.Error()}},
		"run, teardown and report fail": {checkup: checkupStub{failRun: errRun, failTeardown: errTeardown},
			reporter: reporterStub{failReport: errReport, failOnSecondReport: true},
			errors:   []string{errRun.Error(), errTeardown.Error(), errReport.Error()}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			testLauncher := launcher.New(tc.checkup, &tc.reporter)
			err := testLauncher.Run(context.Background())
			for _, expectedErr := range tc.errors {
				assert.ErrorContains(t, err, expectedErr)
			}
		})
	}
}

func TestLauncherSkipTeardownModes(t *testing.T) {
	// Using this custom error to now wether the teardown was called or skipped
	teardownCalledErr := errors.New("teardown called")
	tests := map[string]struct {
		checkup            checkupStub
		reporter           reporterStub
		expectedTeardown   bool
		expectedFailure    bool
		expectedSkipReason config.SkipTeardownMode
	}{
		"skip teardown on failure, with failure": {
			checkup: checkupStub{
				failRun:          errRun,
				skipTeardownMode: config.SkipTeardownOnFailure,
				failTeardown:     teardownCalledErr,
			},
			reporter:           reporterStub{},
			expectedTeardown:   false,
			expectedFailure:    true,
			expectedSkipReason: config.SkipTeardownOnFailure,
		},
		"skip teardown on failure, no failure": {
			checkup: checkupStub{
				skipTeardownMode: config.SkipTeardownOnFailure,
				failTeardown:     teardownCalledErr,
			},
			reporter:           reporterStub{},
			expectedTeardown:   true,
			expectedFailure:    false,
			expectedSkipReason: config.SkipTeardownOnFailure,
		},
		"always skip teardown, with failure": {
			checkup: checkupStub{
				failRun:          errRun,
				skipTeardownMode: config.SkipTeardownAlways,
				failTeardown:     teardownCalledErr,
			},
			reporter:           reporterStub{},
			expectedTeardown:   false,
			expectedFailure:    true,
			expectedSkipReason: config.SkipTeardownAlways,
		},
		"always skip teardown, no failure": {
			checkup: checkupStub{
				skipTeardownMode: config.SkipTeardownAlways,
				failTeardown:     teardownCalledErr,
			},
			reporter:           reporterStub{},
			expectedTeardown:   false,
			expectedFailure:    false,
			expectedSkipReason: config.SkipTeardownAlways,
		},
		"never skip teardown, with failure": {
			checkup: checkupStub{
				failRun:          errRun,
				skipTeardownMode: config.SkipTeardownNever,
				failTeardown:     teardownCalledErr,
			},
			reporter:           reporterStub{},
			expectedTeardown:   true,
			expectedFailure:    true,
			expectedSkipReason: config.SkipTeardownNever,
		},
		"never skip teardown, no failure": {
			checkup: checkupStub{
				skipTeardownMode: config.SkipTeardownNever,
				failTeardown:     teardownCalledErr,
			},
			reporter:           reporterStub{},
			expectedTeardown:   true,
			expectedFailure:    false,
			expectedSkipReason: config.SkipTeardownNever,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			checkup := checkupStub{
				failSetup:        tc.checkup.failSetup,
				failRun:          tc.checkup.failRun,
				failTeardown:     tc.checkup.failTeardown,
				skipTeardownMode: tc.checkup.skipTeardownMode,
			}

			testLauncher := launcher.New(checkup, &tc.reporter)
			err := testLauncher.Run(context.Background())

			if tc.expectedFailure {
				assert.Error(t, err)
			} else if tc.expectedTeardown {
				assert.Equal(t, tc.checkup.failTeardown, teardownCalledErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

type checkupStub struct {
	failSetup        error
	failRun          error
	failTeardown     error
	skipTeardownMode config.SkipTeardownMode
}

func (cs checkupStub) Setup(_ context.Context) error {
	return cs.failSetup
}

func (cs checkupStub) Run(_ context.Context) error {
	return cs.failRun
}

func (cs checkupStub) Teardown(_ context.Context) error {
	return cs.failTeardown
}

func (cs checkupStub) Results() status.Results {
	return status.Results{}
}

func (cs checkupStub) Config() config.Config {
	return config.Config{
		SkipTeardown: cs.skipTeardownMode,
	}
}

type reporterStub struct {
	reportCalls int
	failReport  error
	// The launcher calls the report twice: To mark the start timestamp and
	// then to update the checkup results.
	// Use this flag to cause the second report to fail.
	failOnSecondReport bool
}

func (rs *reporterStub) Report(_ status.Status) error {
	rs.reportCalls++
	if rs.failOnSecondReport && rs.reportCalls == 2 {
		return rs.failReport
	} else if !rs.failOnSecondReport {
		return rs.failReport
	}
	return nil
}
