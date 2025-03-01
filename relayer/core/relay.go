// Copyright 2020 Snowfork
// SPDX-License-Identifier: LGPL-3.0-only

package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/mangata-finance/mangata-bridge/relayer/chain"
	"github.com/mangata-finance/mangata-bridge/relayer/chain/ethereum"
	"github.com/mangata-finance/mangata-bridge/relayer/chain/substrate"

	// "github.com/snowfork/snowbridge/relayer/chain"
	// "github.com/snowfork/snowbridge/relayer/chain/ethereum"
	// "github.com/snowfork/snowbridge/relayer/chain/substrate"

	// "chain"
	// "ethereum"
	// "substrate"
	"github.com/spf13/viper"
	"golang.org/x/sync/errgroup"

	"github.com/ethereum/go-ethereum/common"

	log "github.com/sirupsen/logrus"
)

type Relay struct {
	chains []chain.Chain
}

type Config struct {
	Eth ethereum.Config  `mapstructure:"ethereum"`
	Sub substrate.Config `mapstructure:"substrate"`
}

func NewRelay() (*Relay, error) {

	// channel for messages from ethereum
	ethMessages := make(chan chain.Message, 1)

	// channel for messages from substrate
	subMessages := make(chan chain.Message, 1)

	config, err := loadConfig()
	if err != nil {
		return nil, err
	}

	ethChain, err := ethereum.NewChain(&config.Eth, ethMessages, subMessages)
	if err != nil {
		return nil, err
	}

	subChain, err := substrate.NewChain(&config.Sub, ethMessages, subMessages)
	if err != nil {
		return nil, err
	}

	return &Relay{
		chains: []chain.Chain{ethChain, subChain},
	}, nil
}

func (re *Relay) Start() {

	ctx, cancel := context.WithCancel(context.Background())
	eg, ctx := errgroup.WithContext(ctx)

	// Ensure clean termination upon SIGINT, SIGTERM
	eg.Go(func() error {
		notify := make(chan os.Signal, 1)
		signal.Notify(notify, syscall.SIGINT, syscall.SIGTERM)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case sig := <-notify:
			log.WithField("signal", sig.String()).Info("Received signal")
			cancel()

		}

		return nil
	})

	for _, chain := range re.chains {
		err := chain.Start(ctx, eg)
		if err != nil {
			log.WithFields(log.Fields{
				"chain": chain.Name(),
				"error": err,
			}).Error("Failed to start chain")
			return
		}
		log.WithField("name", chain.Name()).Info("Started chain")
	}

	// Wait until a fatal error or signal is raised
	if err := eg.Wait(); err != nil {
		if !errors.Is(err, context.Canceled) {
			log.WithField("error", err).Error("Encountered an unrecoverable failure")
		}
	}

	// Shutdown chains
	for _, chain := range re.chains {
		chain.Stop()
	}
}

func loadConfig() (*Config, error) {
	var config Config
	err := viper.Unmarshal(&config)
	if err != nil {
		return nil, err
	}

	// Load secrets from environment variables
	var value string
	var ok bool

	value, ok = os.LookupEnv("ARTEMIS_ETHEREUM_KEY")
	if !ok {
		return nil, fmt.Errorf("environment variable not set: ARTEMIS_ETHEREUM_KEY")
	}
	config.Eth.PrivateKey = strings.TrimPrefix(value, "0x")

	value, ok = os.LookupEnv("ARTEMIS_SUBSTRATE_KEY")
	if !ok {
		return nil, fmt.Errorf("environment variable not set: ARTEMIS_SUBSTRATE_KEY")
	}
	config.Sub.PrivateKey = value

	// Copy over Ethereum application addresses to the Substrate config
	config.Sub.Targets = make(map[string][20]byte)
	for k, v := range config.Eth.Apps {
		config.Sub.Targets[k] = common.HexToAddress(v.Address)
	}

	return &config, nil
}
