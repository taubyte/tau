package ethereum

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"time"
	"unicode"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/event"
	"github.com/taubyte/go-sdk/errno"
	eth "github.com/taubyte/go-sdk/ethereum/client/bytes"
	"github.com/taubyte/go-sdk/ethereum/client/logs"
	pubsubIface "github.com/taubyte/tau/core/services/substrate/components/pubsub"
	"github.com/taubyte/tau/core/vm"
)

type channelType uint32

const (
	httpChannel channelType = iota
	pubsubChannel
	p2pChannel
)

func channelTypeFromPrefix(channel string) (channelType channelType, _channel string) {
	if strings.HasPrefix(channel, "http") {
		channelType = httpChannel
	} else if strings.HasPrefix(channel, "pubsub") {
		channelType = pubsubChannel
	} else if strings.HasPrefix(channel, "p2p") {
		channelType = p2pChannel
	}

	return channelType, strings.SplitAfterN(channel, "://", 2)[1]
}

func (f *Factory) W_ethSubscribeContractEvent(
	ctx context.Context,
	module vm.Module,
	clientId,
	contractId,
	eventNamePtr, eventNameLen,
	channelPtr, channelLen,
	ttl uint32, // in seconds
) errno.Error {
	client, err0 := f.getClient(clientId)
	if err0 != 0 {
		return err0
	}

	contract, err0 := client.getContract(contractId)
	if err0 != 0 {
		return err0
	}

	eventName, err0 := f.ReadString(module, eventNamePtr, eventNameLen)
	if err0 != 0 {
		return err0
	}

	contract.eventsLock.RLock()
	ce, ok := contract.events[eventName]
	if !ok || ce == nil {
		return errno.ErrorEthereumEventNotFound
	}
	contract.eventsLock.RUnlock()

	channel, err0 := f.ReadString(module, channelPtr, channelLen)
	if err0 != 0 {
		return err0
	}

	if ce.watcher != nil {
		ce.watcher.lock.RLock()
		_, ok = ce.watcher.published[channel]
		ce.watcher.lock.RUnlock()
		if ok {
			// Already watching
			return 0
		}

		ce.watcher.lock.Lock()
		ce.watcher.published[channel] = make(map[uint64]map[uint]struct{})
		ce.watcher.lock.Unlock()
	}

	channelType, channel := channelTypeFromPrefix(channel)
	switch channelType {
	case httpChannel:
	case pubsubChannel:
		if err := f.pubsubNode.Subscribe(f.parent.Context().Project(), f.parent.Context().Application(), channel); err != nil {
			return errno.ErrorSubscribeFailed
		}
	default:
		return errno.ErrorSubscribeFailed
	}

	//TODO: implement query
	_ctx, _ctxC := context.WithTimeout(context.Background(), time.Duration(ttl)*time.Second)

	// Check if there is an error with watching
	_, _, err := watch(_ctx, ce)
	if err != nil {
		_ctxC()
		return errno.ErrorEthereumWatchEventFailed
	}

	go func(vmCtx vm.Context, pubsubNode pubsubIface.Service) {
		for {
			select {
			case <-_ctx.Done():
				// For go static check
				_ctxC()
				return
			default:
				if err := handleLogChannels(vmCtx, _ctx, 5, pubsubNode, ce, channel, channelType); err != nil {
					if errors.Is(err, errWatch) {
						// if watch failed after passing once exit
						return
					}
				}
			}
		}

	}(f.parent.Context().Clone(_ctx), f.pubsubNode)

	return 0
}

func publish(vmCtx vm.Context, ctx context.Context, pubsubNode pubsubIface.Service, ce *contractEvent, channel string, channelType channelType, logErr error, log *types.Log) error {
	var _log *logs.Log
	var _err string

	if log != nil {
		inputVals, err := ce.event.Inputs.Unpack(log.Data)
		if err != nil {
			_err = err.Error() + ", "
		} else {
			eventInputStruct := reflect.New(ce.structType).Elem()
			for idx, input := range inputVals {
				fieldName := capitalize((ce.event.Inputs)[idx].Name)
				rInput := reflect.ValueOf(input)
				field := eventInputStruct.FieldByName(fieldName)
				if !field.CanSet() {
					_err = "field " + fieldName + "cannot be set, "
				} else {
					field.Set(rInput)
				}
			}

			data, err := json.Marshal(eventInputStruct.Interface())
			if err != nil {
				_err += err.Error() + ", "
			}

			log.Data = data
			_log = toSdkLog(*log)
		}

	}
	if logErr != nil {
		_err += logErr.Error()
	}

	data, err := logs.EventLog{
		Error: _err,
		Log:   _log,
	}.MarshalJSON()
	if err != nil {
		return err
	}

	switch channelType {
	case httpChannel:
		_, err = http.Post(channel, "application/json", bytes.NewBuffer(data))
		return err
	case pubsubChannel:
		return pubsubNode.Publish(ctx, vmCtx.Project(), vmCtx.Application(), channel, data)
	default:
		return errors.New("publishing method not implemented")
	}
}

func watch(ctx context.Context, ce *contractEvent) (logChan chan types.Log, sub event.Subscription, err error) {
	var (
		lastBlock    uint64
		currentBlock uint64
	)

	defer func() {
		if err != nil {
			err = fmt.Errorf("%s with: %w", errWatch, err)
		}
	}()

	if ce.watcher == nil {
		watcher := contractWatcher{
			published: make(map[string]map[uint64]map[uint]struct{}),
		}

		lastBlock, err = ce.parent.client.BlockNumber(ctx)
		if err != nil {
			return
		}

		currentBlock = lastBlock
		watcher.lastBlock = lastBlock

		ce.watcher = &watcher
	} else {
		currentBlock, err = ce.parent.client.BlockNumber(ctx)
		if err != nil {
			return
		}

		lastBlock = ce.watcher.lastBlock
	}

	logChan, sub, err = ce.parent.FilterLogs(
		&bind.FilterOpts{
			Context: ctx,
			Start:   lastBlock,
			End:     &currentBlock,
		}, ce.event.Name,
	)
	if err != nil {
		return
	}

	ce.watcher.lastBlock = currentBlock
	return
}

var errWatch = errors.New("watching event failed")

func handleLogChannels(vmCtx vm.Context, ctx context.Context, ttl int64 /*seconds*/, pubsubNode pubsubIface.Service, ce *contractEvent, channel string, channelType channelType) error {
	logChan, sub, err := watch(ctx, ce)
	if err != nil {
		return err
	}

	errChan := sub.Err()
	logCtx, logCtxC := context.WithTimeout(ctx, time.Duration(ttl)*time.Second)
	defer func() {
		sub.Unsubscribe()
		close(logChan)
		logCtxC()
	}()

	for {
		select {
		case <-logCtx.Done():
			ce.watcher.lock.Lock()
			delete(ce.watcher.published, channel)
			ce.watcher.lock.Unlock()
			return nil
		case err := <-errChan:
			if err != nil {
				sub.Unsubscribe()
				close(logChan)
				return err
			}
		case log, ok := <-logChan:
			if !ok {
				sub.Unsubscribe()
				return errors.New("log channel closed")
			}

			ce.watcher.lock.RLock()
			channelMap, ok := ce.watcher.published[channel]
			ce.watcher.lock.RUnlock()
			if ok {
				blockMap, ok := channelMap[log.BlockNumber]
				if ok {
					if _, ok = blockMap[log.TxIndex]; ok {
						return nil
					}

					ce.watcher.lock.Lock()
					ce.watcher.published[channel][log.BlockNumber][log.TxIndex] = struct{}{}
					ce.watcher.lock.Unlock()
				} else {
					ce.watcher.lock.Lock()
					ce.watcher.published[channel][log.BlockNumber] = make(map[uint]struct{})
					ce.watcher.published[channel][log.BlockNumber][log.TxIndex] = struct{}{}
					ce.watcher.lock.Unlock()
				}
			} else {
				ce.watcher.lock.Lock()
				ce.watcher.published[channel] = make(map[uint64]map[uint]struct{})
				ce.watcher.published[channel][log.BlockNumber] = make(map[uint]struct{})
				ce.watcher.published[channel][log.BlockNumber][log.TxIndex] = struct{}{}
				ce.watcher.lock.Unlock()
			}
			if err = publish(vmCtx, ctx, pubsubNode, ce, channel, channelType, nil, &log); err != nil {
				if err = publish(vmCtx, ctx, pubsubNode, ce, channel, channelType, nil, &log); err != nil {
					publish(vmCtx, ctx, pubsubNode, ce, channel, channelType, fmt.Errorf("publishing log failed with: %w", err), nil)
				}
			}
		}
	}
}

func toSdkLog(log types.Log) *logs.Log {
	topics := make([]*eth.Hash, 0)
	for _, topic := range log.Topics {
		topics = append(topics, eth.BytesToHash(topic.Bytes()))
	}

	return &logs.Log{
		Address:     eth.BytesToAddress(log.Address.Bytes()),
		Topics:      topics,
		Data:        log.Data,
		BlockNumber: log.BlockNumber,
		TxHash:      eth.BytesToHash(log.TxHash.Bytes()),
		TxIndex:     log.TxIndex,
		BlockHash:   eth.BytesToHash(log.BlockHash.Bytes()),
		Index:       log.Index,
		Removed:     log.Removed,
	}
}

// TODO: Do we need to support unicode
func capitalize(str string) string {
	runes := []rune(str)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}
