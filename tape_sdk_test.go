package tape

import (
	"context"
	"fmt"
	"github.com/davidkhala/fabric-common/golang"
	"github.com/davidkhala/goutils"
	"github.com/hyperledger-twgc/tape/pkg/infra/basic"
	tapeEngine "github.com/hyperledger-twgc/tape/pkg/infra/trafficGenerator"
	"github.com/hyperledger/fabric-protos-go-apiv2/common"
	"github.com/hyperledger/fabric-protos-go-apiv2/orderer"
	"github.com/hyperledger/fabric-protos-go-apiv2/peer"
	"github.com/kortschak/utter"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"testing"
)

var logger = logrus.New()

// peer0.icdd
var peer0Icdd = golang.Node{
	Addr:                  "localhost:8051",
	TLSCARoot:             "/home/davidliu/delphi-fabric/config/ca-crypto-config/peerOrganizations/icdd/tlsca/tlsca.icdd-cert.pem",
	SslTargetNameOverride: "peer0.icdd",
}

// peer0.astri.org
var peer0Astri = golang.Node{

	Addr:      "localhost:7051",
	TLSCARoot: "/home/davidliu/delphi-fabric/config/ca-crypto-config/peerOrganizations/astri.org/peers/peer0.astri.org/tls/ca.crt",

	SslTargetNameOverride: "peer0.astri.org",
}

// orderer0.hyperledger
var orderer0 = golang.Node{

	Addr:      "localhost:7050",
	TLSCARoot: "/home/davidliu/delphi-fabric/config/ca-crypto-config/ordererOrganizations/hyperledger/orderers/orderer0.hyperledger/tls/ca.crt",

	SslTargetNameOverride: "orderer0.hyperledger",
}

func nodeRebuild(node golang.Node) basic.Node {
	return basic.Node{
		Addr:                  node.Addr,
		SslTargetNameOverride: node.SslTargetNameOverride,
		TLSCARoot:             node.TLSCARoot,
	}
}

func cryptoRebuild(crypto golang.Crypto) basic.CryptoImpl {
	return basic.CryptoImpl{
		Creator:  crypto.Creator,
		PrivKey:  crypto.PrivKey,
		SignCert: crypto.SignCert,
	}
}

var config = basic.Config{
	//peer0_icdd
	Endorsers:  []basic.Node{nodeRebuild(peer0Icdd)},
	Committers: nil,
	Orderer:    nodeRebuild(orderer0),
	Channel:    "allchannel",
	Chaincode:  "diagnose",
	Args:       []string{"whoami"},
}
var cryptoConfig = golang.CryptoConfig{
	MSPID:    "astriMSP",
	PrivKey:  golang.FindKeyFilesOrPanic("/home/davidliu/delphi-fabric/config/ca-crypto-config/peerOrganizations/astri.org/users/Admin@astri.org/msp/keystore/")[0],
	SignCert: "/home/davidliu/delphi-fabric/config/ca-crypto-config/peerOrganizations/astri.org/users/Admin@astri.org/msp/signcerts/Admin@astri.org-cert.pem",
	// TODO why not support ~
}

func TestPing(t *testing.T) {
	_, err := basic.DialConnection(nodeRebuild(peer0Icdd), logger)
	goutils.PanicError(err)
	_, err = basic.DialConnection(nodeRebuild(peer0Astri), logger)
	goutils.PanicError(err)
	_, err = basic.DialConnection(nodeRebuild(orderer0), logger)
	goutils.PanicError(err)
}
func TestE2E(t *testing.T) {
	var err error
	var proposal *peer.Proposal
	var signed *peer.SignedProposal
	var endorser peer.EndorserClient
	var proposalResponse *peer.ProposalResponse
	var proposalResponses []*peer.ProposalResponse
	var transaction *common.Envelope
	var txResult *orderer.BroadcastResponse
	var peer0, peer1, ordererGrpc *grpc.ClientConn
	var ctx = context.Background()
	defer func() {
		err = peer0.Close()
		err = peer1.Close()
		err = ordererGrpc.Close()
	}()
	// client side
	signer, err := golang.LoadCryptoFrom(cryptoConfig)
	goutils.PanicError(err)
	// client side end
	proposal, txid, err := golang.CreateProposal(
		signer.Creator,
		config.Channel,
		config.Chaincode,
		"",
		nil,
		config.Args...,
	)
	goutils.PanicError(err)
	// server side end
	var tapeSigner = cryptoRebuild(*signer)
	signed, err = tapeEngine.SignProposal(proposal, &tapeSigner)
	goutils.PanicError(err)
	// server side
	// peer0.icdd
	peer0, err = peer0Icdd.AsGRPCClient()
	goutils.PanicError(err)
	peer1, err = peer0Astri.AsGRPCClient()
	endorser = golang.EndorserFrom(peer0)
	goutils.PanicError(err)

	proposalResponse, err = endorser.ProcessProposal(ctx, signed)
	goutils.PanicError(err)

	if proposalResponse.Response.Status < 200 || proposalResponse.Response.Status >= 400 {
		err = fmt.Errorf("Err processing proposal: %s, status: %d, message: %s,\n", err, proposalResponse.Response.Status, proposalResponse.Response.Message)
		goutils.PanicError(err)
	}

	utter.Dump(string(proposalResponse.Response.Payload))

	proposalResponses = []*peer.ProposalResponse{proposalResponse}
	// server side end
	transaction, err = tapeEngine.CreateSignedTx(signed, &tapeSigner, proposalResponses)
	goutils.PanicError(err)
	// server side
	ordererGrpc, err = orderer0.AsGRPCClient()
	goutils.PanicError(err)
	var committer = golang.Committer{
		AtomicBroadcastClient: golang.CommitterFrom(ordererGrpc),
	}
	err = committer.Setup()
	goutils.PanicError(err)

	txResult, err = committer.SendRecv(transaction)
	goutils.PanicError(err)
	if txResult.Status != common.Status_SUCCESS {
		panic(txResult)
	}
	// server side end
	utter.Dump(txid)

	var eventer = golang.EventerFrom(ctx, peer1)
	var seek = eventer.AsTransactionListener(txid)
	signedEvent, err := seek.SignBy(config.Channel, &tapeSigner)
	goutils.PanicError(err)
	_, err = eventer.SendRecv(signedEvent)
	goutils.PanicError(err)
	utter.Dump(eventer.ReceiptData)
}
