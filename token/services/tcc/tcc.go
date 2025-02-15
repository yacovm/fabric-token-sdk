/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/
package tcc

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"runtime/debug"

	"github.com/hyperledger/fabric-chaincode-go/shim"
	pb "github.com/hyperledger/fabric-protos-go/peer"
	"github.com/pkg/errors"

	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/flogging"

	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/hash"
	view2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/hyperledger-labs/fabric-token-sdk/token"
	"github.com/hyperledger-labs/fabric-token-sdk/token/services/vault/translator"
	token2 "github.com/hyperledger-labs/fabric-token-sdk/token/token"
)

var logger = flogging.MustGetLogger("token-sdk.tcc")

const (
	InvokeFunction            = "invoke"
	QueryPublicParamsFunction = "queryPublicParams"
	AddAuditorFunction        = "addAuditor"
	AddIssuerFunction         = "addIssuer"
	AddCertifierFunction      = "addCertifier"
	QueryTokensFunctions      = "queryTokens"

	PublicParamsPathVarEnv = "PUBLIC_PARAMS_FILE_PATH"
)

type SetupAction struct {
	SetupParameters []byte
}

func (a *SetupAction) GetSetupParameters() ([]byte, error) {
	return a.SetupParameters, nil
}

type allIssuersValid struct{}

func (i *allIssuersValid) Validate(creator view2.Identity, tokenType string) error {
	return nil
}

//go:generate counterfeiter -o mock/validator.go -fake-name Validator . Validator

type Validator interface {
	UnmarshallAndVerify(ledger token.Ledger, binding string, raw []byte) ([]interface{}, error)
}

//go:generate counterfeiter -o mock/public_parameters_manager.go -fake-name PublicParametersManager . PublicParametersManager

type PublicParametersManager interface {
	AddIssuer(issuer []byte) ([]byte, error)
	SetAuditor(auditor []byte) ([]byte, error)
	SetCertifier(certifier []byte) ([]byte, error)
}

type TokenChaincode struct {
	LogLevel                string
	Validator               Validator
	PublicParametersManager PublicParametersManager

	PPDigest             []byte
	TokenServicesFactory func([]byte) (PublicParametersManager, Validator, error)
}

func (cc *TokenChaincode) Init(stub shim.ChaincodeStubInterface) pb.Response {
	logger.Infof("init token chaincode...")

	params := cc.readParamsFromFile()
	if params == "" {
		if len(Params) == 0 {
			args := stub.GetArgs()
			// args[0] public parameters
			if len(args) != 2 {
				return shim.Error("length of provided arguments != 2")
			}

			if string(args[0]) != "init" {
				return shim.Error("expected init function")
			}
			params = string(args[1])
		} else {
			params = Params
		}
	}

	ppRaw, err := base64.StdEncoding.DecodeString(params)
	if err != nil {
		return shim.Error("failed to decode public parameters: " + err.Error())
	}

	issuingValidator := &allIssuersValid{}
	rwset := &rwsWrapper{stub: stub}
	w := translator.New(issuingValidator, "", rwset, "")
	action := &SetupAction{
		SetupParameters: ppRaw,
	}

	err = w.Write(action)
	if err != nil {
		return shim.Error(err.Error())
	}

	return shim.Success(nil)
}

func (cc *TokenChaincode) Invoke(stub shim.ChaincodeStubInterface) (res pb.Response) {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("invoke triggered panic: %s\n%s\n", r, debug.Stack())
			res = shim.Error(fmt.Sprintf("failed responding [%s]", r))
		} else {
			logger.Infof("execution terminated with status [%d]", res.Status)
		}
	}()

	args := stub.GetArgs()
	switch l := len(args); l {
	case 0:
		return shim.Error("missing parameters")
	default:
		logger.Infof("running function [%s]", string(args[0]))
		switch f := string(args[0]); f {
		case InvokeFunction:
			if len(args) != 2 {
				return shim.Error("empty token request")
			}
			return cc.invoke(args[1], stub)
		case QueryPublicParamsFunction:
			return cc.queryPublicParams(stub)
		case AddAuditorFunction:
			if len(args) != 2 {
				return shim.Error("invalid add auditor request")
			}
			return cc.addAuditor(args[1], stub)
		case AddIssuerFunction:
			if len(args) != 2 {
				return shim.Error("request to add issuer is empty")
			}
			return cc.addIssuer(args, stub)
		case AddCertifierFunction:
			if len(args) != 2 {
				return shim.Error("request to add certifier is empty")
			}
			return cc.addCertifier(args[1], stub)
		case QueryTokensFunctions:
			if len(args) != 2 {
				return shim.Error("request to retrieve tokens is empty")
			}
			return cc.queryTokens(args[1], stub)
		default:
			return shim.Error(fmt.Sprintf("function not [%s] recognized", f))
		}
	}
}

func (cc *TokenChaincode) readParamsFromFile() string {
	publicParamsPath := os.Getenv(PublicParamsPathVarEnv)
	if publicParamsPath == "" {
		fmt.Println("no PUBLIC_PARAMS_FILE_PATH provided")
		return ""
	}

	fmt.Println("reading " + publicParamsPath + " ...")
	paramsAsBytes, err := ioutil.ReadFile(publicParamsPath)
	if err != nil {
		fmt.Println(fmt.Sprintf(
			"unable to read file %s (%s). continue looking pub params from init args or cc", publicParamsPath, err.Error(),
		))
		return ""
	}

	return base64.StdEncoding.EncodeToString(paramsAsBytes)
}

func (cc *TokenChaincode) publicParametersManager(stub shim.ChaincodeStubInterface) (PublicParametersManager, error) {
	if err := cc.init(stub); err != nil {
		return nil, err
	}
	return cc.PublicParametersManager, nil
}

func (cc *TokenChaincode) validator(stub shim.ChaincodeStubInterface) (Validator, error) {
	if err := cc.init(stub); err != nil {
		return nil, err
	}
	return cc.Validator, nil
}

func (cc *TokenChaincode) init(stub shim.ChaincodeStubInterface) error {
	logger.Infof("reading public parameters...")

	rwset := &rwsWrapper{stub: stub}
	issuingValidator := &allIssuersValid{}
	w := translator.New(issuingValidator, stub.GetTxID(), rwset, "")
	ppRaw, err := w.ReadSetupParameters()
	if err != nil {
		return errors.Wrapf(err, "failed to retrieve public parameters")
	}
	logger.Infof("public parameters read [%d]", len(ppRaw))
	if len(ppRaw) == 0 {
		return errors.Errorf("public parameters are not initiliazed yet")
	}
	hash := sha256.New()
	n, err := hash.Write(ppRaw)
	if n != len(ppRaw) {
		return errors.New("failed hashing public parameters, bytes not consumed")
	}
	if err != nil {
		return errors.Wrap(err, "failed hashing public parameters")
	}
	digest := hash.Sum(nil)
	if len(cc.PPDigest) != 0 && cc.Validator != nil && bytes.Equal(digest, cc.PPDigest) {
		logger.Infof("no need instantiate public parameter manager and validator, already set")
		return nil
	}

	logger.Infof("instantiate public parameter manager and validator...")
	ppm, validator, err := cc.TokenServicesFactory(ppRaw)
	logger.Infof("instantiate public parameter manager and validator done with err [%v]", err)
	if err != nil {
		return errors.Wrap(err, "failed to instantiate public parameter manager and validator")
	}
	cc.PublicParametersManager = ppm
	cc.Validator = validator
	cc.PPDigest = digest

	return nil
}

func (cc *TokenChaincode) invoke(raw []byte, stub shim.ChaincodeStubInterface) pb.Response {
	validator, err := cc.validator(stub)
	if err != nil {
		return shim.Error(err.Error())
	}

	// Verify
	actions, err := validator.UnmarshallAndVerify(stub, stub.GetTxID(), raw)
	if err != nil {
		return shim.Error("failed to verify token request: " + err.Error())
	}

	// Write
	rwset := &rwsWrapper{stub: stub}
	issuingValidator := &allIssuersValid{}
	w := translator.New(issuingValidator, stub.GetTxID(), rwset, "")
	for _, action := range actions {
		err = w.Write(action)
		if err != nil {
			return shim.Error("failed to write token action: " + err.Error())
		}
	}
	err = w.CommitTokenRequest(raw)
	if err != nil {
		return shim.Error("failed to write token request:" + err.Error())
	}
	return shim.Success(nil)
}

func (cc *TokenChaincode) queryPublicParams(stub shim.ChaincodeStubInterface) pb.Response {
	rwset := &rwsWrapper{stub: stub}
	issuingValidator := &allIssuersValid{}
	w := translator.New(issuingValidator, stub.GetTxID(), rwset, "")
	raw, err := w.ReadSetupParameters()
	if err != nil {
		shim.Error("failed to retrieve public parameters: " + err.Error())
	}
	if len(raw) == 0 {
		return shim.Error("need to initialize public parameters")
	}
	return shim.Success(raw)
}

func (cc *TokenChaincode) addIssuer(args [][]byte, stub shim.ChaincodeStubInterface) pb.Response {
	ppm, err := cc.publicParametersManager(stub)
	if err != nil {
		return shim.Error(err.Error())
	}

	raw, err := ppm.AddIssuer(args[1])
	if err != nil {
		return shim.Error("failed to serialize public parameters")
	}

	issuingValidator := &allIssuersValid{}
	rwset := &rwsWrapper{stub: stub}
	w := translator.New(issuingValidator, "", rwset, "")
	setupAction := &SetupAction{SetupParameters: raw}
	if err := w.Write(setupAction); err != nil {
		return shim.Error("failed to update issuing policy: " + err.Error())
	}

	return shim.Success(nil)
}

func (cc *TokenChaincode) addAuditor(auditor []byte, stub shim.ChaincodeStubInterface) pb.Response {
	// todo authenticate creator of addAuditor request
	// todo only admins are allowed to add auditors

	logger.Infof("add auditor [%s]", hash.Hashable(auditor))

	logger.Infof("load public parameters manager...")
	ppm, err := cc.publicParametersManager(stub)
	if err != nil {
		logger.Errorf("failed loading public parameters manager [%s]", err)
		return shim.Error(err.Error())
	}
	logger.Infof("set auditor...")
	raw, err := ppm.SetAuditor(auditor)
	if err != nil {
		logger.Errorf("failed setting auditor [%s]", err)
		return shim.Error(err.Error())
	}
	logger.Infof("new public params created [%d]", len(raw))

	// TODO: seems redundant
	logger.Infof("translate...")
	w := &translator.Translator{RWSet: &rwsWrapper{stub: stub}}
	setupAction := &SetupAction{SetupParameters: raw}
	if err := w.Write(setupAction); err != nil {
		return shim.Error("failed to write auditor key")
	}
	logger.Infof("translate...done.")
	return shim.Success(raw)
}

func (cc *TokenChaincode) addCertifier(certifier []byte, stub shim.ChaincodeStubInterface) pb.Response {
	// todo authenticate creator of addCertifier request
	// todo only admins are allowed to add certifier

	ppm, err := cc.publicParametersManager(stub)
	if err != nil {
		return shim.Error(err.Error())
	}
	raw, err := ppm.SetCertifier(certifier)
	if err != nil {
		return shim.Error(err.Error())
	}

	w := &translator.Translator{RWSet: &rwsWrapper{stub: stub}}
	setupAction := &SetupAction{SetupParameters: raw}
	if err := w.Write(setupAction); err != nil {
		return shim.Error("failed to write auditor key")
	}
	return shim.Success(raw)
}

func (cc *TokenChaincode) queryTokens(idsRaw []byte, stub shim.ChaincodeStubInterface) pb.Response {
	var ids []*token2.Id
	if err := json.Unmarshal(idsRaw, &ids); err != nil {
		logger.Errorf("failed unmarshalling tokens ids: [%s]", err)
		return shim.Error(err.Error())
	}

	logger.Debugf("query tokens [%v]...", ids)

	rwset := &rwsWrapper{stub: stub}
	issuingValidator := &allIssuersValid{}
	w := translator.New(issuingValidator, stub.GetTxID(), rwset, "")
	res, err := w.QueryTokens(ids)
	if err != nil {
		logger.Errorf("failed query tokens [%v]: [%s]", ids, err)
		return shim.Error(fmt.Sprintf("failed query tokens [%v]: [%s]", ids, err))
	}
	raw, err := json.Marshal(res)
	if err != nil {
		logger.Errorf("failed marshalling tokens: [%s]", err)
		return shim.Error(fmt.Sprintf("failed marshalling tokens: [%s]", err))
	}
	return shim.Success(raw)
}
