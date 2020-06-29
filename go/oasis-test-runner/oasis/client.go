package oasis

import (
	"fmt"

	registry "github.com/oasisprotocol/oasis-core/go/registry/api"
	storageClient "github.com/oasisprotocol/oasis-core/go/storage/client"
)

// Client is an Oasis client node.
type Client struct {
	Node

	consensusPort uint16
}

// ClientCfg is the Oasis client node provisioning configuration.
type ClientCfg struct {
	NodeCfg
}

func (client *Client) startNode() error {
	args := newArgBuilder().
		debugDontBlameOasis().
		debugAllowTestKeys().
		workerCertificateRotation(true).
		tendermintDebugDisableCheckTx(client.consensus.DisableCheckTx).
		tendermintPrune(client.consensus.PruneNumKept).
		tendermintCoreListenAddress(client.consensusPort).
		storageBackend(storageClient.BackendName).
		appendNetwork(client.net).
		appendSeedNodes(client.net).
		runtimeTagIndexerBackend("bleve")
	for _, v := range client.net.runtimes {
		if v.kind != registry.KindCompute {
			continue
		}
		args = args.runtimeSupported(v.id).
			appendRuntimePruner(&v.pruner)
	}

	if err := client.net.startOasisNode(&client.Node, nil, args); err != nil {
		return fmt.Errorf("oasis/client: failed to launch node %s: %w", client.Name, err)
	}

	return nil
}

// Start starts an Oasis node.
func (client *Client) Start() error {
	return client.startNode()
}

// NewClient provisions a new client node and adds it to the network.
func (net *Network) NewClient(cfg *ClientCfg) (*Client, error) {
	clientName := fmt.Sprintf("client-%d", len(net.clients))

	clientDir, err := net.baseDir.NewSubDir(clientName)
	if err != nil {
		net.logger.Error("failed to create client subdir",
			"err", err,
			"client_name", clientName,
		)
		return nil, fmt.Errorf("oasis/client: failed to create client subdir: %w", err)
	}

	client := &Client{
		Node: Node{
			Name:      clientName,
			net:       net,
			dir:       clientDir,
			consensus: cfg.Consensus,
		},
		consensusPort: net.nextNodePort,
	}
	client.doStartNode = client.startNode

	net.clients = append(net.clients, client)
	net.nextNodePort++

	return client, nil
}
