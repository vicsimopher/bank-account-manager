package thanosnotif

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	ethereumTypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/tokamak-network/tokamak-thanos/op-bindings/bindings"
	"github.com/tokamak-network/tokamak-thanos/op-bindings/bindingspreview"
	"github.com/tokamak-network/tokamak-thanos/op-bindings/predeploys"

	"github.com/tokamak-network/tokamak-thanos-event-listener/internal/pkg/erc20"
	"github.com/tokamak-network/tokamak-thanos-event-listener/pkg/log"
)

func (p *App) notifyRawInitWithdrawalEvent(vLog *ethereumTypes.Log, event *bindings.L2ToL1MessagePasserMessagePassed) (string, string, error) {
	title := fmt.Sprintf("[" + p.cfg.Network + "] [Withdrawal Intialized]")
	text := fmt.Sprintf("Tx: "+p.cfg.L2ExplorerUrl+"/tx/%s\n"+
		"Withrawal Hash: %s\n"+
		"Sender: %s\n"+
		"Target: %s\n"+
		"Value: %d\n"+
		"GasLimit: %d\n"+
		"Data: %s\n"+
		"Nonce: %d",
		vLog.TxHash.Hex(),
		hex.EncodeToString(event.WithdrawalHash[:]),
		event.Sender,
		event.Target,
		event.Value,
		event.GasLimit,
		hex.EncodeToString(event.Data),
		event.Nonce)
	return title, text, nil
}

func (p *App) handleMessagePassed(vLog *ethereumTypes.Log) (string, string, error) {
	log.GetLogger().Infow("Got Withdrawal finalized Event", "event", vLog)
	l2ToL1MessagePasserFilterer, err := p.getL2ToL1MessagePasserFilterers()
	if err != nil {
		return "", "", err
	}
	event, err := l2ToL1MessagePasserFilterer.ParseMessagePassed(*vLog)
	if err != nil {
		log.GetLogger().Errorw("MessagePassed event parsing fail", "error", err)
		return "", "", err
	}

	if hex.EncodeToString(event.Sender[:]) == predeploys.L2CrossDomainMessenger[2:] {
		messengerABI, err := abi.JSON(strings.NewReader(bindings.L1CrossDomainMessengerABI))
		if err != nil {
			goto defaultCase
		}
		in, err := UnpackInputData(messengerABI, "relayMessage", event.Data)
		if err != nil {
			goto defaultCase
		}
		nonce, sender, target, value, minGasLimit, message := in[0].(*big.Int), in[1].(common.Address), in[2].(common.Address), in[3], in[4], in[5].([]byte)
		log.GetLogger().Debug("[ Sent message ]", nonce, sender, target, value, minGasLimit, message)
		if target.Hex() == p.cfg.L1StandardBridge || target.Hex() == p.cfg.L1UsdcBridge {
			log.GetLogger().Debug("Not handled in MessagePassed")
			return "", "", errors.New(" handle in other function")
		}
	}
defaultCase:
	return p.notifyRawInitWithdrawalEvent(vLog, event)
}

func (p *App) handleWithdrawalFinalized(vLog *ethereumTypes.Log) (string, string, error) {
	log.GetLogger().Infow("Got Withdrawal finalized Event", "event", vLog)

	finalizedTx, _, err := p.l1Client.GetClient().TransactionByHash(context.Background(), vLog.TxHash)
	if err != nil {
		return "", "", err
	}
	finalizedCallData := finalizedTx.Data()
	portalABI, err := abi.JSON(strings.NewReader(bindingspreview.OptimismPortal2ABI))
	if err != nil {
		return "", "", err
	}

	_unpackedInput, err := UnpackInputData(portalABI, "finalizeWithdrawalTransaction", finalizedCallData)
	if err != nil {
		return "", "", err
	}

	unpackedInput := _unpackedInput[0].(struct {
		Nonce    *big.Int       "json:\"nonce\""
		Sender   common.Address "json:\"sender\""
		Target   common.Address "json:\"target\""
		Value    *big.Int       "json:\"value\""
		GasLimit *big.Int       "json:\"gasLimit\""
		Data     []uint8        "json:\"data\""
	})
	if unpackedInput.Target.Hex() == p.cfg.L1XDM {
		messengerABI, err := abi.JSON(strings.NewReader(bindings.L1CrossDomainMessengerABI))
		if err != nil {
			goto defaultCase
		}
		in, err := UnpackInputData(messengerABI, "relayMessage", unpackedInput.Data)
		if err != nil {
			goto defaultCase
		}
		nonce, sender, target, value, minGasLimit, message := in[0].(*big.Int), in[1].(common.Address), in[2].(common.Address), in[3], in[4], in[5].([]byte)
		log.GetLogger().Debug("[Relay Message ]", nonce, sender, target, value, minGasLimit, message)
		if target.Hex() == p.cfg.L1StandardBridge || target.Hex() == p.cfg.L1UsdcBridge {
			return "", "", errors.New("handle in other function")
		}
	}

defaultCase:
	optimismPortalFilterer, err := p.getOptimismPortalFilterers()
	if err != nil {
		return "", "", err
	}
	event, err := optimismPortalFilterer.ParseWithdrawalFinalized(*vLog)
	if err != nil {
		log.GetLogger().Errorw("WithdrawalFinalized event parsing fail", "error", err)
		return "", "", err
	}
	// Slack notify title and text
	title := fmt.Sprintf("[" + p.cfg.Network + "] [Withdrawal Finalized]")
	text := fmt.Sprintf("Tx: "+p.cfg.L1ExplorerUrl+"/tx/%s\nWithrawal Hash: %s\nStatus: %b", vLog.TxHash, hex.EncodeToString(event.WithdrawalHash[:]), event.Success)

	return title, text, nil
}

func (p *App) withdrawalNativeTokenFinalizedEvent(vLog *ethereumTypes.Log) (string, string, error) {
	log.GetLogger().Infow("Got TON Withdrawal Event", "event", vLog)

	l1BridgeFilterer, _, err := p.getBridgeFilterers()
	if err != nil {
		return "", "", err
	}

	event, err := l1BridgeFilterer.ParseNativeTokenBridgeFinalized(*vLog)
	if err != nil {
		log.GetLogger().Errorw("NativeTokenBridgeFinalized event log parsing fail", "error", err)
		return "", "", err
	}

	ethWith := bindings.L1StandardBridgeNativeTokenBridgeFinalized{
		From:   event.From,
		To:     event.To,
		Amount: event.Amount,
	}

	Amount := formatAmount(ethWith.Amount, 18)

	// Slack notify title and text
	title := fmt.Sprintf("[" + p.cfg.Network + "] [TON Withdrawal Finalized]")
	text := fmt.Sprintf("Tx: "+p.cfg.L1ExplorerUrl+"/tx/%s\nFrom: "+p.cfg.L2ExplorerUrl+"/address/%s\nTo: "+p.cfg.L1ExplorerUrl+"/address/%s\nAmount: %s TON", vLog.TxHash, ethWith.From, ethWith.To, Amount)

	return title, text, nil
}

func (p *App) withdrawalETHFinalizedEvent(vLog *ethereumTypes.Log) (string, string, error) {
	log.GetLogger().Infow("Got ETH Withdrawal Event", "event", vLog)

	l1BridgeFilterer, _, err := p.getBridgeFilterers()
	if err != nil {
		return "", "", err
	}

	event, err := l1BridgeFilterer.ParseETHWithdrawalFinalized(*vLog)
	if err != nil {
		log.GetLogger().Errorw("ETHWithdrawalFinalized event log parsing fail", "error", err)
		return "", "", err
	}

	ethWith := bindings.L1StandardBridgeETHWithdrawalFinalized{
		From:   event.From,
		To:     event.To,
		Amount: event.Amount,
	}

	Amount := formatAmount(ethWith.Amount, 18)

	// Slack notify title and text
	title := fmt.Sprintf("[" + p.cfg.Network + "] [ETH Withdrawal Finalized]")
	text := fmt.Sprintf("Tx: "+p.cfg.L1ExplorerUrl+"/tx/%s\nFrom: "+p.cfg.L2ExplorerUrl+"/address/%s\nTo: "+p.cfg.L1ExplorerUrl+"/address/%s\nAmount: %s ETH", vLog.TxHash, ethWith.From, ethWith.To, Amount)

	return title, text, nil
}

func (p *App) withdrawalERC20FinalizedEvent(vLog *ethereumTypes.Log) (string, string, error) {
	log.GetLogger().Infow("Got ERC20 Withdrawal Event", "event", vLog)

	l1BridgeFilterer, _, err := p.getBridgeFilterers()
	if err != nil {
		return "", "", err
	}

	event, err := l1BridgeFilterer.ParseERC20WithdrawalFinalized(*vLog)
	if err != nil {
		log.GetLogger().Errorw("ERC20WithdrawalFinalized event parsing fail", "error", err)
		return "", "", err
	}

	erc20With := bindings.L1StandardBridgeERC20WithdrawalFinalized{
		L1Token: event.L1Token,
		L2Token: event.L2Token,
		From:    event.From,
		To:      event.To,
		Amount:  event.Amount,
	}

	// get symbol and decimals
	l1Token := erc20With.L1Token
	l1TokenInfo, found := p.l1TokensInfo[l1Token.Hex()]
	if !found {
		newToken, err := erc20.FetchTokenInfo(p.l1Client, l1Token.Hex())
		if err != nil || newToken == nil {
			log.GetLogger().Errorw("Token info not found for address", "l1Token", l1Token.Hex())
			return "", "", err
		}
		l1TokenInfo = newToken
		p.mu.Lock()
		p.l1TokensInfo[l1Token.Hex()] = l1TokenInfo
		p.mu.Unlock()
	}

	tokenSymbol := l1TokenInfo.Symbol
	tokenDecimals := l1TokenInfo.Decimals

	amount := formatAmount(erc20With.Amount, tokenDecimals)

	// Slack notify title and text
	var title string

	isTON := l1TokenInfo.Symbol == "TON"

	if isTON {
		title = fmt.Sprintf("[" + p.cfg.Network + "] [TON Withdrawal Finalized]")
	} else {
		title = fmt.Sprintf("[" + p.cfg.Network + "] [ERC-20 Withdrawal Finalized]")
	}
	text := fmt.Sprintf("Tx: "+p.cfg.L1ExplorerUrl+"/tx/%s\nFrom: "+p.cfg.L2ExplorerUrl+"/address/%s\nTo: "+p.cfg.L1ExplorerUrl+"/address/%s\nL1Token: "+p.cfg.L1ExplorerUrl+"/token/%s\nL2Token: "+p.cfg.L2ExplorerUrl+"/token/%s\nAmount: %s %s", vLog.TxHash, erc20With.From, erc20With.To, erc20With.L1Token, erc20With.L2Token, amount, tokenSymbol)

	return title, text, nil
}

func (p *App) withdrawalInitiatedEvent(vLog *ethereumTypes.Log) (string, string, error) {
	log.GetLogger().Infow("Got L2 Withdrawal Event", "event", vLog)

	_, l2BridgeFilterer, err := p.getBridgeFilterers()
	if err != nil {
		return "", "", err
	}

	event, err := l2BridgeFilterer.ParseWithdrawalInitiated(*vLog)
	if err != nil {
		log.GetLogger().Errorw("WithdrawalInitiated event parsing fail", "error", err)
		return "", "", err
	}

	l2With := bindings.L2StandardBridgeWithdrawalInitiated{
		L1Token: event.L1Token,
		L2Token: event.L2Token,
		From:    event.From,
		To:      event.To,
		Amount:  event.Amount,
	}

	l2Token := l2With.L2Token

	l2TokenInfo, found := p.l2TokensInfo[l2Token.Hex()]
	if !found {
		newToken, err := erc20.FetchTokenInfo(p.l2Client, l2Token.Hex())
		if err != nil || newToken == nil {
			log.GetLogger().Errorw("Token info not found for address", "l2Token", l2Token.Hex())
			return "", "", err
		}
		l2TokenInfo = newToken
		p.mu.Lock()
		p.l2TokensInfo[l2Token.Hex()] = l2TokenInfo
		p.mu.Unlock()
	}

	if l2TokenInfo == nil {
		return "", "", fmt.Errorf("l2TokenInfo not found")
	}

	tokenSymbol := l2TokenInfo.Symbol
	tokenDecimals := l2TokenInfo.Decimals
	amount := formatAmount(l2With.Amount, tokenDecimals)

	var title string
	var text string

	isETH := l2TokenInfo.Symbol == "ETH"
	isTON := l2TokenInfo.Symbol == "TON"

	if isETH {
		title = fmt.Sprintf("[" + p.cfg.Network + "] [ETH Withdrawal Initialized]")
		text = fmt.Sprintf("Tx: "+p.cfg.L2ExplorerUrl+"/tx/%s\nFrom: "+p.cfg.L2ExplorerUrl+"/address/%s\nTo: "+p.cfg.L1ExplorerUrl+"/address/%s\nL1Token: ETH\nL2Token: "+p.cfg.L2ExplorerUrl+"/token/%s\nAmount: %s %s", vLog.TxHash, l2With.From, l2With.To, l2With.L2Token, amount, tokenSymbol)
	} else if isTON {
		title = fmt.Sprintf("[" + p.cfg.Network + "] [TON Withdrawal Initialized]")
		text = fmt.Sprintf("Tx: "+p.cfg.L2ExplorerUrl+"/tx/%s\nFrom: "+p.cfg.L2ExplorerUrl+"/address/%s\nTo: "+p.cfg.L1ExplorerUrl+"/address/%s\nL1Token: "+p.cfg.L1ExplorerUrl+"/token/%s\nL2Token: "+p.cfg.L2ExplorerUrl+"/token/%s\nAmount: %s %s", vLog.TxHash, l2With.From, l2With.To, l2With.L1Token, l2With.L2Token, amount, tokenSymbol)
	} else {
		title = fmt.Sprintf("[" + p.cfg.Network + "] [ERC-20 Withdrawal Initialized]")
		text = fmt.Sprintf("Tx: "+p.cfg.L2ExplorerUrl+"/tx/%s\nFrom: "+p.cfg.L2ExplorerUrl+"/address/%s\nTo: "+p.cfg.L1ExplorerUrl+"/address/%s\nL1Token: "+p.cfg.L1ExplorerUrl+"/token/%s\nL2Token: "+p.cfg.L2ExplorerUrl+"/token/%s\nAmount: %s %s", vLog.TxHash, l2With.From, l2With.To, l2With.L1Token, l2With.L2Token, amount, tokenSymbol)
	}

	return title, text, nil
}

func (p *App) withdrawalUsdcFinalizedEvent(vLog *ethereumTypes.Log) (string, string, error) {
	log.GetLogger().Infow("Got L1 USDC Withdrawal Event", "event", vLog)

	l1UsdcBridgeFilterer, _, err := p.getUSDCBridgeFilterers()
	if err != nil {
		return "", "", err
	}

	event, err := l1UsdcBridgeFilterer.ParseERC20WithdrawalFinalized(*vLog)
	if err != nil {
		log.GetLogger().Errorw("USDC WithdrawalFinalized event parsing fail", "error", err)
		return "", "", err
	}

	l1UsdcWith := bindings.L1UsdcBridgeERC20WithdrawalFinalized{
		L1Token: event.L1Token,
		L2Token: event.L2Token,
		From:    event.From,
		To:      event.To,
		Amount:  event.Amount,
	}

	Amount := formatAmount(l1UsdcWith.Amount, 6)

	// Slack notify title and text
	title := fmt.Sprintf("[" + p.cfg.Network + "] [USDC Withdrawal Finalized]")
	text := fmt.Sprintf("Tx: "+p.cfg.L1ExplorerUrl+"/tx/%s\nFrom: "+p.cfg.L2ExplorerUrl+"/address/%s\nTo: "+p.cfg.L1ExplorerUrl+"/address/%s\nL1Token: "+p.cfg.L1ExplorerUrl+"/token/%s\nL2Token: "+p.cfg.L2ExplorerUrl+"/token/%s\nAmount: %s USDC", vLog.TxHash, l1UsdcWith.From, l1UsdcWith.To, l1UsdcWith.L1Token, l1UsdcWith.L2Token, Amount)

	return title, text, nil
}

func (p *App) withdrawalUsdcInitiatedEvent(vLog *ethereumTypes.Log) (string, string, error) {
	log.GetLogger().Infow("Got L2 USDC Withdrawal Event", "event", vLog)

	_, l2UsdcBridgeFilterer, err := p.getUSDCBridgeFilterers()
	if err != nil {
		log.GetLogger().Errorw("Failed to get USDC bridge filters", "error", err)
		return "", "", err
	}

	event, err := l2UsdcBridgeFilterer.ParseWithdrawalInitiated(*vLog)
	if err != nil {
		log.GetLogger().Errorw("Failed to parse the USDC WithdrawalInitiated event", "error", err)
		return "", "", err
	}

	l2UsdcWith := bindings.L2UsdcBridgeWithdrawalInitiated{
		L1Token: event.L1Token,
		L2Token: event.L2Token,
		From:    event.From,
		To:      event.To,
		Amount:  event.Amount,
	}

	Amount := formatAmount(l2UsdcWith.Amount, 6)

	title := fmt.Sprintf("[" + p.cfg.Network + "] [USDC Withdrawal Initialized]")
	text := fmt.Sprintf("Tx: "+p.cfg.L2ExplorerUrl+"/tx/%s\nFrom: "+p.cfg.L2ExplorerUrl+"/address/%s\nTo: "+p.cfg.L1ExplorerUrl+"/address/%s\nL1Token: "+p.cfg.L1ExplorerUrl+"/token/%s\nL2Token: "+p.cfg.L2ExplorerUrl+"/token/%s\nAmount: %s USDC", vLog.TxHash, l2UsdcWith.From, l2UsdcWith.To, l2UsdcWith.L1Token, l2UsdcWith.L2Token, Amount)

	return title, text, nil
}
