package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/oasisprotocol/oasis-core/go/common/crypto/signature"
	"github.com/oasisprotocol/oasis-core/go/common/quantity"
	"github.com/oasisprotocol/oasis-core/go/oasis-node/cmd/common"
	"github.com/oasisprotocol/oasis-core/go/oasis-node/cmd/common/consensus"
	"github.com/oasisprotocol/oasis-core/go/oasis-node/cmd/common/flags"
	"github.com/oasisprotocol/oasis-core/go/oasis-node/cmd/common/grpc"
	"github.com/oasisprotocol/oasis-core/go/oasis-node/cmd/stake"
	"github.com/oasisprotocol/oasis-core/go/oasis-test-runner/env"
	"github.com/oasisprotocol/oasis-core/go/oasis-test-runner/oasis"
	"github.com/oasisprotocol/oasis-core/go/oasis-test-runner/oasis/cli"
	"github.com/oasisprotocol/oasis-core/go/oasis-test-runner/scenario"
	"github.com/oasisprotocol/oasis-core/go/staking/api"
)

const (
	// Init balance in the genesis block.
	initBalance = 100_000_000_000

	// Test transfer amount.
	transferAmount = 1000

	// Test burn amount.
	burnAmount = 2000

	// Test escrow amount.
	escrowAmount = 3000

	// Test reclaim escrow shares.
	reclaimEscrowShares = 1234

	// Transaction fee amount.
	feeAmount = 10

	// Transaction fee gas.
	feeGas = 10000

	// Testing source account public key (hex-encoded).
	srcPubkeyHex = "4ea5328f943ef6f66daaed74cb0e99c3b1c45f76307b425003dbc7cb3638ed35"

	// Testing destination account public key (hex-encoded).
	dstPubkeyHex = "5ea5328f943ef6f66daaed74cb0e99c3b1c45f76307b425003dbc7cb3638ed35"

	// Testing escrow account public key (hex-encoded).
	escrowPubkeyHex = "6ea5328f943ef6f66daaed74cb0e99c3b1c45f76307b425003dbc7cb3638ed35"
)

var (
	// Testing source account address.
	srcAddress = api.NewAddress(signature.NewPublicKey(srcPubkeyHex))

	// Testing destination account address.
	dstAddress = api.NewAddress(signature.NewPublicKey(dstPubkeyHex))

	// Testing escrow account address.
	escrowAddress = api.NewAddress(signature.NewPublicKey(escrowPubkeyHex))

	// StakeCLI is the staking scenario.
	StakeCLI scenario.Scenario = &stakeCLIImpl{
		E2E: *NewE2E("stake-cli"),
	}
)

type stakeCLIImpl struct {
	E2E
}

func (sc *stakeCLIImpl) Clone() scenario.Scenario {
	return &stakeCLIImpl{
		E2E: sc.E2E.Clone(),
	}
}

func (sc *stakeCLIImpl) Fixture() (*oasis.NetworkFixture, error) {
	f, err := sc.E2E.Fixture()
	if err != nil {
		return nil, err
	}

	// We will mock epochs for reclaiming the escrow.
	f.Network.EpochtimeMock = true

	// Enable some features in the staking system that we'll test.
	f.Network.StakingGenesis = "tests/fixture-data/stake-cli/staking-genesis.json"

	return f, nil
}

func (sc *stakeCLIImpl) Run(childEnv *env.Env) error {
	if err := sc.Net.Start(); err != nil {
		return err
	}

	ctx := context.Background()
	sc.Logger.Info("waiting for nodes to register")
	if err := sc.Net.Controller().WaitNodesRegistered(ctx, 3); err != nil {
		return fmt.Errorf("waiting for nodes to register: %w", err)
	}
	sc.Logger.Info("nodes registered")

	cli := cli.New(childEnv, sc.Net, sc.Logger)

	// Common staking info.
	if err := sc.getInfo(childEnv); err != nil {
		return err
	}

	// List of account addresses.
	addresses, err := sc.listAccountAddresses(childEnv)
	if err != nil {
		return err
	}
	// In the genesis block, only one account should have a balance.
	if len(addresses) < 1 {
		return fmt.Errorf(
			"wrong number of accounts in initial list: %d, expected at least: %d. Accounts: %s",
			len(addresses), 1, addresses,
		)
	}

	// Ensure the source account address is in the list.
	var found bool
	for _, addr := range addresses {
		if bytes.Equal(addr[:], srcAddress[:]) {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("src account address not found in initial list: %s", srcAddress)
	}

	// Run the tests

	// Ensure converting public keys to staking account addresses works.
	pubkey2AddressTestVectors := []struct {
		publicKeyText string
		addressText   string
		expectError   bool
	}{
		{signature.NewPublicKey(srcPubkeyHex).String(), srcAddress.String(), false},
		{signature.NewPublicKey(dstPubkeyHex).String(), dstAddress.String(), false},
		{signature.NewPublicKey(escrowPubkeyHex).String(), escrowAddress.String(), false},
		// Empty public key.
		{"", "", true},
		// Invalid public key.
		{"BadPubKey=", "", true},
	}
	sc.Logger.Info("test converting public keys to staking account addresses")
	for _, vector := range pubkey2AddressTestVectors {
		err = sc.testPubkey2Address(childEnv, vector.publicKeyText, vector.addressText)
		if err != nil && !vector.expectError {
			return fmt.Errorf("unexpected pubkey2address error: %w", err)
		}
		if err == nil && vector.expectError {
			return fmt.Errorf("pubkey2address for public key '%s' should error", vector.publicKeyText)
		}
	}

	// Transfer
	if err = sc.testTransfer(childEnv, cli, srcAddress, dstAddress); err != nil {
		return fmt.Errorf("error while running Transfer test: %w", err)
	}

	// Burn
	if err = sc.testBurn(childEnv, cli, srcAddress); err != nil {
		return fmt.Errorf("error while running Burn test: %w", err)
	}

	// Escrow
	if err = sc.testEscrow(childEnv, cli, srcAddress, escrowAddress); err != nil {
		return fmt.Errorf("error while running Escrow test: %w", err)
	}

	// ReclaimEscrow
	if err = sc.testReclaimEscrow(childEnv, cli, srcAddress, escrowAddress); err != nil {
		return fmt.Errorf("error while running ReclaimEscrow test: %w", err)
	}

	// AmendCommissionSchedule
	if err = sc.testAmendCommissionSchedule(childEnv, cli, srcAddress); err != nil {
		return fmt.Errorf("error while running AmendCommissionSchedule: %w", err)
	}

	// Stop the network.
	sc.Logger.Info("stopping the network")
	sc.Net.Stop()

	return nil
}

func (sc *stakeCLIImpl) testPubkey2Address(childEnv *env.Env, publicKeyText string, addressText string) error {
	args := []string{
		"stake", "pubkey2address",
		"--" + stake.CfgPublicKey, publicKeyText,
	}

	out, err := cli.RunSubCommandWithOutput(childEnv, sc.Logger, "info", sc.Net.Config().NodeBinary, args)
	if err != nil {
		return fmt.Errorf("failed to convert public key to address: error: %w output: %s", err, out.String())
	}

	var addr api.Address
	if err = addr.UnmarshalText(bytes.TrimSpace(out.Bytes())); err != nil {
		return err
	}

	if addr.String() != addressText {
		return fmt.Errorf("pubkey2address converted public key %s to address %s (expected address: %s)",
			publicKeyText, addr, addressText,
		)
	}

	return nil
}

// testTransfer tests transfer of transferAmount base units from src to dst.
func (sc *stakeCLIImpl) testTransfer(childEnv *env.Env, cli *cli.Helpers, src api.Address, dst api.Address) error {
	transferTxPath := filepath.Join(childEnv.Dir(), "stake_transfer.json")
	if err := sc.genTransferTx(childEnv, transferAmount, 0, dst, transferTxPath); err != nil {
		return err
	}
	if err := sc.showTx(childEnv, transferTxPath); err != nil {
		return err
	}
	if err := sc.checkBalance(childEnv, src, initBalance); err != nil {
		return err
	}
	if err := sc.checkBalance(childEnv, dst, 0); err != nil {
		return err
	}

	if err := cli.Consensus.SubmitTx(transferTxPath); err != nil {
		return err
	}

	if err := sc.checkBalance(childEnv, src, initBalance-transferAmount-feeAmount); err != nil {
		return err
	}
	if err := sc.checkBalance(childEnv, dst, transferAmount); err != nil {
		return err
	}
	accounts, err := sc.listAccountAddresses(childEnv)
	if err != nil {
		return err
	}
	if len(accounts) < 2 {
		return fmt.Errorf("post-transfer stake list wrong number of accounts: %d, expected at least: %d. Accounts: %s", len(accounts), 2, accounts)
	}

	return nil
}

// testBurn tests burning of burnAmount base units owned by src.
func (sc *stakeCLIImpl) testBurn(childEnv *env.Env, cli *cli.Helpers, src api.Address) error {
	burnTxPath := filepath.Join(childEnv.Dir(), "stake_burn.json")
	if err := sc.genBurnTx(childEnv, burnAmount, 1, burnTxPath); err != nil {
		return err
	}
	if err := sc.showTx(childEnv, burnTxPath); err != nil {
		return err
	}

	if err := cli.Consensus.SubmitTx(burnTxPath); err != nil {
		return err
	}

	if err := sc.checkBalance(childEnv, src, initBalance-transferAmount-burnAmount-2*feeAmount); err != nil {
		return err
	}
	accounts, err := sc.listAccountAddresses(childEnv)
	if err != nil {
		return err
	}
	if len(accounts) < 2 {
		return fmt.Errorf("post-burn stake list wrong number of accounts: %d, expected at least: %d", len(accounts), 2)
	}

	return nil
}

// testEscrow tests escrowing escrowAmount base units from src to dst.
func (sc *stakeCLIImpl) testEscrow(childEnv *env.Env, cli *cli.Helpers, src api.Address, escrow api.Address) error {
	escrowTxPath := filepath.Join(childEnv.Dir(), "stake_escrow.json")
	if err := sc.genEscrowTx(childEnv, escrowAmount, 2, escrow, escrowTxPath); err != nil {
		return err
	}
	if err := sc.showTx(childEnv, escrowTxPath); err != nil {
		return err
	}

	if err := cli.Consensus.SubmitTx(escrowTxPath); err != nil {
		return err
	}

	if err := sc.checkBalance(childEnv, src, initBalance-transferAmount-burnAmount-escrowAmount-3*feeAmount); err != nil {
		return err
	}
	if err := sc.checkEscrowBalance(childEnv, escrow, escrowAmount); err != nil {
		return err
	}
	accounts, err := sc.listAccountAddresses(childEnv)
	if err != nil {
		return err
	}
	if len(accounts) < 3 {
		return fmt.Errorf("post-escrow stake list wrong number of accounts: %d, expected at least: %d", len(accounts), 3)
	}

	return nil
}

// testReclaimEscrow test reclaiming reclaimEscrowShares shares from an escrow account.
func (sc *stakeCLIImpl) testReclaimEscrow(childEnv *env.Env, cli *cli.Helpers, src api.Address, escrow api.Address) error {
	reclaimEscrowTxPath := filepath.Join(childEnv.Dir(), "stake_reclaim_escrow.json")
	if err := sc.genReclaimEscrowTx(childEnv, reclaimEscrowShares, 3, escrow, reclaimEscrowTxPath); err != nil {
		return err
	}
	if err := sc.showTx(childEnv, reclaimEscrowTxPath); err != nil {
		return err
	}

	if err := cli.Consensus.SubmitTx(reclaimEscrowTxPath); err != nil {
		return err

	}
	// Advance epochs to trigger reclaim processing.
	if err := sc.Net.Controller().SetEpoch(context.Background(), 1); err != nil {
		return fmt.Errorf("failed to set epoch: %w", err)
	}

	// Since we are the only ones who put stake into the escrow account and there was no slashing,
	// we can expect the reclaimed escrow amount to equal the number of reclaimed escrow shares.
	var reclaimEscrowAmount int64 = reclaimEscrowShares
	if err := sc.checkBalance(childEnv, src, initBalance-transferAmount-burnAmount-escrowAmount+reclaimEscrowAmount-4*feeAmount); err != nil {
		return err
	}
	if err := sc.checkEscrowBalance(childEnv, escrow, escrowAmount-reclaimEscrowAmount); err != nil {
		return err
	}
	accounts, err := sc.listAccountAddresses(childEnv)
	if err != nil {
		return err
	}
	if len(accounts) < 3 {
		return fmt.Errorf("post-reclaim-escrow stake list wrong number of accounts: %d, expected: %d", len(accounts), 3)
	}

	return nil
}

func mustInitQuantity(i int64) (q quantity.Quantity) {
	if err := q.FromInt64(i); err != nil {
		panic(fmt.Sprintf("FromInt64: %+v", err))
	}
	return
}

func (sc *stakeCLIImpl) testAmendCommissionSchedule(childEnv *env.Env, cli *cli.Helpers, src api.Address) error {
	amendCommissionScheduleTxPath := filepath.Join(childEnv.Dir(), "amend_commission_schedule.json")
	if err := sc.genAmendCommissionScheduleTx(childEnv, 4, &api.CommissionSchedule{
		Rates: []api.CommissionRateStep{
			{
				Start: 40,
				Rate:  mustInitQuantity(50_000),
			},
		},
		Bounds: []api.CommissionRateBoundStep{
			{
				Start:   40,
				RateMin: mustInitQuantity(0),
				RateMax: mustInitQuantity(100_000),
			},
		},
	}, amendCommissionScheduleTxPath); err != nil {
		return err
	}
	if err := sc.showTx(childEnv, amendCommissionScheduleTxPath); err != nil {
		return err
	}

	if err := cli.Consensus.SubmitTx(amendCommissionScheduleTxPath); err != nil {
		return err
	}

	// todo: check that it was applied

	return nil
}

func (sc *stakeCLIImpl) getInfo(childEnv *env.Env) error {
	sc.Logger.Info("querying common staking info")
	args := []string{
		"stake", "info",
		"--" + grpc.CfgAddress, "unix:" + sc.Net.Validators()[0].SocketPath(),
	}

	out, err := cli.RunSubCommandWithOutput(childEnv, sc.Logger, "info", sc.Net.Config().NodeBinary, args)
	if err != nil {
		return fmt.Errorf("failed to query common staking info: error: %w output: %s", err, out.String())
	}
	return nil
}

func (sc *stakeCLIImpl) listAccountAddresses(childEnv *env.Env) ([]api.Address, error) {
	sc.Logger.Info("listing all account addresses")
	args := []string{
		"stake", "list",
		"--" + grpc.CfgAddress, "unix:" + sc.Net.Validators()[0].SocketPath(),
	}
	out, err := cli.RunSubCommandWithOutput(childEnv, sc.Logger, "list", sc.Net.Config().NodeBinary, args)
	if err != nil {
		return nil, fmt.Errorf("failed to list account addresses: error: %w output: %s",
			err, out.String(),
		)
	}
	addressesText := strings.Split(out.String(), "\n")

	var addresses []api.Address
	for _, addrText := range addressesText {
		// Ignore last newline.
		if addrText == "" {
			continue
		}

		var addr api.Address
		if err = addr.UnmarshalText([]byte(addrText)); err != nil {
			return nil, fmt.Errorf("failed to unmarshal account address: %w", err)
		}
		addresses = append(addresses, addr)
	}

	return addresses, nil
}

func (sc *stakeCLIImpl) getAccountInfo(childEnv *env.Env, src api.Address) (*api.Account, error) {
	sc.Logger.Info("checking account balance", stake.CfgAccountAddr, src.String())
	args := []string{
		"stake", "account", "info",
		"--" + stake.CfgAccountAddr, src.String(),
		"--" + grpc.CfgAddress, "unix:" + sc.Net.Validators()[0].SocketPath(),
	}

	out, err := cli.RunSubCommandWithOutput(childEnv, sc.Logger, "info", sc.Net.Config().NodeBinary, args)
	if err != nil {
		return nil, fmt.Errorf("failed to check account info: error: %w output: %s", err, out.String())
	}

	var acct api.Account
	if err = json.Unmarshal(out.Bytes(), &acct); err != nil {
		return nil, err
	}

	return &acct, nil
}

func (sc *stakeCLIImpl) checkBalance(childEnv *env.Env, src api.Address, expected int64) error {
	ai, err := sc.getAccountInfo(childEnv, src)
	if err != nil {
		return err
	}

	var q quantity.Quantity
	if err = q.FromBigInt(big.NewInt(expected)); err != nil {
		return err
	}
	if ai.General.Balance.Cmp(&q) != 0 {
		return fmt.Errorf("checkBalance: wrong general balance of account. Expected %s got %s", q, ai.General.Balance)
	}

	return nil
}

func (sc *stakeCLIImpl) checkEscrowBalance(childEnv *env.Env, src api.Address, expected int64) error {
	ai, err := sc.getAccountInfo(childEnv, src)
	if err != nil {
		return err
	}

	var q quantity.Quantity
	if err = q.FromBigInt(big.NewInt(expected)); err != nil {
		return err
	}
	if ai.Escrow.Active.Balance.Cmp(&q) != 0 {
		return fmt.Errorf("checkEscrowBalance: wrong escrow balance of account. Expected %s got %s", q, ai.Escrow.Active.Balance)
	}

	return nil
}

func (sc *stakeCLIImpl) showTx(childEnv *env.Env, txPath string) error {
	sc.Logger.Info("pretty printing generated transaction")

	args := []string{
		"consensus", "show_tx",
		"--" + consensus.CfgTxFile, txPath,
		"--" + common.CfgDebugAllowTestKeys,
		"--" + flags.CfgDebugDontBlameOasis,
		"--" + flags.CfgGenesisFile, sc.Net.GenesisPath(),
	}
	if out, err := cli.RunSubCommandWithOutput(childEnv, sc.Logger, "show_tx", sc.Net.Config().NodeBinary, args); err != nil {
		return fmt.Errorf("showTx: failed to show tx: error: %w, output: %s", err, out.String())
	}
	return nil
}

func (sc *stakeCLIImpl) genTransferTx(childEnv *env.Env, amount int, nonce int, dst api.Address, txPath string) error {
	sc.Logger.Info("generating stake transfer tx", stake.CfgTransferDestination, dst)

	args := []string{
		"stake", "account", "gen_transfer",
		"--" + stake.CfgAmount, strconv.Itoa(amount),
		"--" + consensus.CfgTxNonce, strconv.Itoa(nonce),
		"--" + consensus.CfgTxFile, txPath,
		"--" + stake.CfgTransferDestination, dst.String(),
		"--" + consensus.CfgTxFeeAmount, strconv.Itoa(feeAmount),
		"--" + consensus.CfgTxFeeGas, strconv.Itoa(feeGas),
		"--" + flags.CfgDebugDontBlameOasis,
		"--" + flags.CfgDebugTestEntity,
		"--" + common.CfgDebugAllowTestKeys,
		"--" + flags.CfgGenesisFile, sc.Net.GenesisPath(),
	}
	if out, err := cli.RunSubCommandWithOutput(childEnv, sc.Logger, "gen_transfer", sc.Net.Config().NodeBinary, args); err != nil {
		return fmt.Errorf("genTransferTx: failed to generate transfer tx: error: %w output: %s", err, out.String())
	}
	return nil
}

func (sc *stakeCLIImpl) genBurnTx(childEnv *env.Env, amount int, nonce int, txPath string) error {
	sc.Logger.Info("generating stake burn tx")

	args := []string{
		"stake", "account", "gen_burn",
		"--" + stake.CfgAmount, strconv.Itoa(amount),
		"--" + consensus.CfgTxNonce, strconv.Itoa(nonce),
		"--" + consensus.CfgTxFile, txPath,
		"--" + consensus.CfgTxFeeAmount, strconv.Itoa(feeAmount),
		"--" + consensus.CfgTxFeeGas, strconv.Itoa(feeGas),
		"--" + flags.CfgDebugDontBlameOasis,
		"--" + flags.CfgDebugTestEntity,
		"--" + common.CfgDebugAllowTestKeys,
		"--" + flags.CfgGenesisFile, sc.Net.GenesisPath(),
	}
	if out, err := cli.RunSubCommandWithOutput(childEnv, sc.Logger, "gen_burn", sc.Net.Config().NodeBinary, args); err != nil {
		return fmt.Errorf("genBurnTx: failed to generate burn tx: error: %w output: %s", err, out.String())
	}
	return nil
}

func (sc *stakeCLIImpl) genEscrowTx(childEnv *env.Env, amount int, nonce int, escrow api.Address, txPath string) error {
	sc.Logger.Info("generating stake escrow tx", "stake.CfgEscrowAccount", escrow)

	args := []string{
		"stake", "account", "gen_escrow",
		"--" + stake.CfgAmount, strconv.Itoa(amount),
		"--" + consensus.CfgTxNonce, strconv.Itoa(nonce),
		"--" + consensus.CfgTxFile, txPath,
		"--" + stake.CfgEscrowAccount, escrow.String(),
		"--" + consensus.CfgTxFeeAmount, strconv.Itoa(feeAmount),
		"--" + consensus.CfgTxFeeGas, strconv.Itoa(feeGas),
		"--" + flags.CfgDebugDontBlameOasis,
		"--" + flags.CfgDebugTestEntity,
		"--" + common.CfgDebugAllowTestKeys,
		"--" + flags.CfgGenesisFile, sc.Net.GenesisPath(),
	}
	if out, err := cli.RunSubCommandWithOutput(childEnv, sc.Logger, "gen_escrow", sc.Net.Config().NodeBinary, args); err != nil {
		return fmt.Errorf("genEscrowTx: failed to generate escrow tx: error: %w output: %s", err, out.String())
	}
	return nil
}

func (sc *stakeCLIImpl) genReclaimEscrowTx(childEnv *env.Env, shares int, nonce int, escrow api.Address, txPath string) error {
	sc.Logger.Info("generating stake reclaim escrow tx", stake.CfgEscrowAccount, escrow)

	args := []string{
		"stake", "account", "gen_reclaim_escrow",
		"--" + stake.CfgShares, strconv.Itoa(shares),
		"--" + consensus.CfgTxNonce, strconv.Itoa(nonce),
		"--" + consensus.CfgTxFile, txPath,
		"--" + stake.CfgEscrowAccount, escrow.String(),
		"--" + consensus.CfgTxFeeAmount, strconv.Itoa(feeAmount),
		"--" + consensus.CfgTxFeeGas, strconv.Itoa(feeGas),
		"--" + flags.CfgDebugDontBlameOasis,
		"--" + flags.CfgDebugTestEntity,
		"--" + common.CfgDebugAllowTestKeys,
		"--" + flags.CfgGenesisFile, sc.Net.GenesisPath(),
	}
	if out, err := cli.RunSubCommandWithOutput(childEnv, sc.Logger, "gen_reclaim_escrow", sc.Net.Config().NodeBinary, args); err != nil {
		return fmt.Errorf("genReclaimEscrowTx: failed to generate reclaim escrow tx: error: %w output: %s", err, out.String())
	}
	return nil
}

func (sc *stakeCLIImpl) genAmendCommissionScheduleTx(childEnv *env.Env, nonce int, cs *api.CommissionSchedule, txPath string) error {
	sc.Logger.Info("generating stake amend commission schedule tx", "commission_schedule", cs)

	args := []string{
		"stake", "account", "gen_amend_commission_schedule",
		"--" + consensus.CfgTxNonce, strconv.Itoa(nonce),
		"--" + consensus.CfgTxFile, txPath,
		"--" + consensus.CfgTxFeeAmount, strconv.Itoa(feeAmount),
		"--" + consensus.CfgTxFeeGas, strconv.Itoa(feeGas),
		"--" + flags.CfgDebugDontBlameOasis,
		"--" + flags.CfgDebugTestEntity,
		"--" + common.CfgDebugAllowTestKeys,
		"--" + flags.CfgGenesisFile, sc.Net.GenesisPath(),
	}
	for _, step := range cs.Rates {
		args = append(args, "--"+stake.CfgCommissionScheduleRates, fmt.Sprintf("%d/%d", step.Start, step.Rate.ToBigInt()))
	}
	for _, step := range cs.Bounds {
		args = append(args, "--"+stake.CfgCommissionScheduleBounds, fmt.Sprintf("%d/%d/%d", step.Start, step.RateMin.ToBigInt(), step.RateMax.ToBigInt()))
	}
	if out, err := cli.RunSubCommandWithOutput(childEnv, sc.Logger, "gen_amend_commission_schedule", sc.Net.Config().NodeBinary, args); err != nil {
		return fmt.Errorf("genAmendCommissionScheduleTx: failed to generate amend commission schedule tx: error: %w output: %s", err, out.String())
	}
	return nil
}
