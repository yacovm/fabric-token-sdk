/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/
package driver

import (
	fabric2 "github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
	view2 "github.com/hyperledger-labs/fabric-smart-client/platform/view"

	"github.com/hyperledger-labs/fabric-token-sdk/token/api"
	"github.com/hyperledger-labs/fabric-token-sdk/token/core"
	"github.com/hyperledger-labs/fabric-token-sdk/token/core/identity"
	"github.com/hyperledger-labs/fabric-token-sdk/token/core/identity/fabric"
	"github.com/hyperledger-labs/fabric-token-sdk/token/core/zkatdlog/crypto"
	"github.com/hyperledger-labs/fabric-token-sdk/token/core/zkatdlog/crypto/ppm"
	"github.com/hyperledger-labs/fabric-token-sdk/token/core/zkatdlog/crypto/validator"
	zkatdlog "github.com/hyperledger-labs/fabric-token-sdk/token/core/zkatdlog/nogh"
	"github.com/hyperledger-labs/fabric-token-sdk/token/services/vault"
)

type Driver struct {
}

func (d *Driver) PublicParametersFromBytes(params []byte) (api.PublicParameters, error) {
	pp, err := crypto.NewPublicParamsFromBytes(params)
	if err != nil {
		return nil, err
	}
	return pp, nil
}

func (d *Driver) NewTokenService(sp view2.ServiceProvider, publicParamsFetcher api.PublicParamsFetcher, network string, channel api.Channel, namespace string) (api.TokenManagerService, error) {
	nodeIdentity := view2.GetIdentityProvider(sp).DefaultIdentity()
	return zkatdlog.NewTokenService(
		channel,
		namespace,
		sp,
		publicParamsFetcher,
		&zkatdlog.VaultTokenCommitmentLoader{TokenVault: vault.NewVault(sp, channel, namespace).QueryEngine()},
		vault.NewVault(sp, channel, namespace).QueryEngine(),
		identity.NewProvider(
			sp,
			map[api.IdentityUsage]identity.Mapper{
				api.IssuerRole:  fabric.NewMapper(fabric.X509MSPIdentity, nodeIdentity, fabric2.GetFabricNetworkService(sp, network).LocalMembership()),
				api.AuditorRole: fabric.NewMapper(fabric.X509MSPIdentity, nodeIdentity, fabric2.GetFabricNetworkService(sp, network).LocalMembership()),
				api.OwnerRole:   fabric.NewMapper(fabric.IdemixMSPIdentity, nodeIdentity, fabric2.GetFabricNetworkService(sp, network).LocalMembership()),
			},
		),
	)
}

func (d *Driver) NewValidator(params api.PublicParameters) (api.Validator, error) {
	return validator.New(params.(*crypto.PublicParams)), nil
}

func (d *Driver) NewPublicParametersManager(params api.PublicParameters) (api.PublicParamsManager, error) {
	return ppm.New(params.(*crypto.PublicParams)), nil
}

func init() {
	core.Register(crypto.DLogPublicParameters, &Driver{})
}
