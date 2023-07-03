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
	t.Run("report fails", func(t *testing.T) {
		testLauncher := launcher.New(checkupStub{}, &reporterStub{failReport: errReport})
		assert.ErrorContains(t, testLauncher.Run(context.Background()), errReport.Error())
	})

	t.Run("setup fails", func(t *testing.T) {
		testLauncher := launcher.New(checkupStub{failSetup: errSetup}, &reporterStub{})
		assert.ErrorContains(t, testLauncher.Run(context.Background()), errSetup.Error())
	})

	t.Run("setup and 2nd report fail", func(t *testing.T) {
		testLauncher := launcher.New(
			checkupStub{failSetup: errSetup},
			&reporterStub{failReport: errReport, failOnSecondReport: true},
		)
		err := testLauncher.Run(context.Background())
		assert.ErrorContains(t, err, errSetup.Error())
		assert.ErrorContains(t, err, errReport.Error())
	})

	t.Run("run fails", func(t *testing.T) {
		testLauncher := launcher.New(checkupStub{failRun: errRun}, &reporterStub{})
		assert.ErrorContains(t, testLauncher.Run(context.Background()), errRun.Error())
	})

	t.Run("teardown fails", func(t *testing.T) {
		testLauncher := launcher.New(checkupStub{failTeardown: errTeardown}, &reporterStub{})
		assert.ErrorContains(t, testLauncher.Run(context.Background()), errTeardown.Error())
	})

	t.Run("run and report fail", func(t *testing.T) {
		testLauncher := launcher.New(
			checkupStub{failRun: errRun},
			&reporterStub{failReport: errReport, failOnSecondReport: true},
		)
		err := testLauncher.Run(context.Background())
		assert.ErrorContains(t, err, errRun.Error())
		assert.ErrorContains(t, err, errReport.Error())
	})

	t.Run("teardown and report fail", func(t *testing.T) {
		testLauncher := launcher.New(
			checkupStub{failTeardown: errTeardown},
			&reporterStub{failReport: errReport, failOnSecondReport: true},
		)
		err := testLauncher.Run(context.Background())
		assert.ErrorContains(t, err, errTeardown.Error())
		assert.ErrorContains(t, err, errReport.Error())
	})

	t.Run("run, teardown and report fail", func(t *testing.T) {
		testLauncher := launcher.New(
			checkupStub{failRun: errRun, failTeardown: errTeardown},
			&reporterStub{failReport: errReport, failOnSecondReport: true},
		)
		err := testLauncher.Run(context.Background())
		assert.ErrorContains(t, err, errRun.Error())
		assert.ErrorContains(t, err, errTeardown.Error())
		assert.ErrorContains(t, err, errReport.Error())
	})
}

type checkupStub struct {
	failSetup    error
	failRun      error
	failTeardown error
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
