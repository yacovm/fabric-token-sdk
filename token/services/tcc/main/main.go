/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/
package main

import (
	"fmt"
	"os"

	"github.com/hyperledger/fabric-chaincode-go/shim"

	"github.com/hyperledger-labs/fabric-token-sdk/token"
	_ "github.com/hyperledger-labs/fabric-token-sdk/token/core/fabtoken/driver"
	_ "github.com/hyperledger-labs/fabric-token-sdk/token/core/zkatdlog/nogh/driver"
	"github.com/hyperledger-labs/fabric-token-sdk/token/services/tcc"
)

type serverConfig struct {
	CCID      string
	CCaddress string
	LogLevel  string
}

func main() {
	config := serverConfig{
		CCID:      os.Getenv("CHAINCODE_ID"),
		CCaddress: os.Getenv("CHAINCODE_SERVER_ADDRESS"),
		LogLevel:  os.Getenv("CHAINCODE_LOG_LEVEL"),
	}

	if config.CCID == "" || config.CCaddress == "" {
		fmt.Println("CC ID or CC address is empty... Running as usual...")
		if os.Getenv("DEVMODE_ENABLED") != "" {
			fmt.Println("starting up in devmode...")
		}
		err := shim.Start(
			&tcc.TokenChaincode{
				TokenServicesFactory: func(bytes []byte) (tcc.PublicParametersManager, tcc.Validator, error) {
					return token.NewServicesFromPublicParams(bytes)
				},
			},
		)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Exiting chaincode: %s", err)
			os.Exit(2)
		}
	} else {
		fmt.Println("Token Chaincode CCID : " + config.CCID)
		fmt.Println("Token Chaincode address : " + config.CCaddress)
		fmt.Println("Running Token Chaincode as service ...")
		server := &shim.ChaincodeServer{
			CCID:    config.CCID,
			Address: config.CCaddress,
			CC: &tcc.TokenChaincode{
				TokenServicesFactory: func(bytes []byte) (tcc.PublicParametersManager, tcc.Validator, error) {
					return token.NewServicesFromPublicParams(bytes)
				},
				LogLevel: config.LogLevel,
			},
			TLSProps: shim.TLSProperties{
				// TODO : enable TLS
				Disabled: true,
			},
		}
		err := server.Start()
		if err != nil {
			fmt.Printf("Error starting Token Chaincode: %s", err)
		}
	}
}
