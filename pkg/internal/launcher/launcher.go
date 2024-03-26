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

package launcher

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/status"
)

type checkup interface {
	Setup(ctx context.Context) error
	Run(ctx context.Context) error
	Teardown(ctx context.Context) error
	Results() status.Results
}

type reporter interface {
	Report(status.Status) error
}

type Launcher struct {
	checkup  checkup
	reporter reporter
}

func New(checkup checkup, reporter reporter) Launcher {
	return Launcher{
		checkup:  checkup,
		reporter: reporter,
	}
}

func (l Launcher) Run(ctx context.Context) (runErr error) {
	var runStatus status.Status
	runStatus.StartTimestamp = time.Now()

	if err := l.reporter.Report(runStatus); err != nil {
		return err
	}

	defer func() {
		runStatus.CompletionTimestamp = time.Now()
		runStatus.Results = l.checkup.Results()
		if err := l.reporter.Report(runStatus); err != nil {
			runStatus.FailureReason = append(runStatus.FailureReason, err.Error())
		}
		runErr = failureReason(runStatus)
	}()

	if err := l.checkup.Setup(ctx); err != nil {
		runStatus.FailureReason = append(runStatus.FailureReason, err.Error())
		return err
	}

	defer func() {
		if err := l.checkup.Teardown(context.Background()); err != nil {
			runStatus.FailureReason = append(runStatus.FailureReason, err.Error())
		}
	}()

	if err := l.checkup.Run(ctx); err != nil {
		runStatus.FailureReason = append(runStatus.FailureReason, err.Error())
		return err
	}

	return nil
}

func failureReason(sts status.Status) error {
	if len(sts.FailureReason) > 0 {
		return errors.New(strings.Join(sts.FailureReason, ", "))
	}
	return nil
}
