package runtime

import (
	"context"

	"github.com/oasisprotocol/oasis-core/go/oasis-test-runner/env"
	"github.com/oasisprotocol/oasis-core/go/oasis-test-runner/oasis"
	"github.com/oasisprotocol/oasis-core/go/oasis-test-runner/scenario"
)

var (
	// GasFeesRuntimes is the runtime gas fees scenario.
	GasFeesRuntimes scenario.Scenario = &gasFeesRuntimesImpl{
		runtimeImpl: *newRuntimeImpl("gas-fees/runtimes", "", nil),
	}
)

// gasPrice is the gas price used during the test.
const gasPrice = 1

type gasFeesRuntimesImpl struct {
	runtimeImpl
}

func (sc *gasFeesRuntimesImpl) Clone() scenario.Scenario {
	return &gasFeesRuntimesImpl{
		runtimeImpl: *sc.runtimeImpl.Clone().(*runtimeImpl),
	}
}

func (sc *gasFeesRuntimesImpl) Fixture() (*oasis.NetworkFixture, error) {
	f, err := sc.runtimeImpl.Fixture()
	if err != nil {
		return nil, err
	}

	// Use deterministic identities as we need to allocate funds to nodes.
	f.Network.DeterministicIdentities = true
	// Give our nodes some stake.
	f.Network.StakingGenesis = "tests/fixture-data/gas-fees-runtimes/staking-genesis.json"
	// Update validators to require fee payments.
	for i := range f.Validators {
		f.Validators[i].Consensus.MinGasPrice = gasPrice
		f.Validators[i].Consensus.SubmissionGasPrice = gasPrice
	}
	// Update all other nodes to use a specific gas price.
	for i := range f.Keymanagers {
		f.Keymanagers[i].Consensus.SubmissionGasPrice = gasPrice
	}
	for i := range f.StorageWorkers {
		f.StorageWorkers[i].Consensus.SubmissionGasPrice = gasPrice
	}
	for i := range f.ComputeWorkers {
		f.ComputeWorkers[i].Consensus.SubmissionGasPrice = gasPrice
	}
	for i := range f.ByzantineNodes {
		f.ByzantineNodes[i].Consensus.SubmissionGasPrice = gasPrice
	}

	return f, nil
}

func (sc *gasFeesRuntimesImpl) Run(childEnv *env.Env) error {
	if err := sc.Net.Start(); err != nil {
		return err
	}

	ctx := context.Background()

	// Wait for all nodes to be synced before we proceed.
	if err := sc.waitNodesSynced(); err != nil {
		return err
	}

	// Submit a runtime transaction to check whether transaction processing works.
	sc.Logger.Info("submitting transaction to runtime")
	if err := sc.submitKeyValueRuntimeInsertTx(ctx, runtimeID, "hello", "non-free world"); err != nil {
		return err
	}

	return nil
}
