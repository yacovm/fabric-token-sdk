/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/
package anonym

import (
	"encoding/json"

	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/hash"

	"github.com/hyperledger-labs/fabric-smart-client/platform/view/api"

	"github.com/hyperledger-labs/fabric-token-sdk/token/core/math/gurvy/bn256"
	"github.com/hyperledger-labs/fabric-token-sdk/token/core/zkatdlog/crypto/o2omp"
	"github.com/pkg/errors"
)

type Authorization struct {
	Type  *bn256.G1 // commitment to issuer's secret key and type (g_0^SK*g_1^type*h^r')
	Token *bn256.G1 // commitment to type and Value of a token (g_0^type*g_1^Value*h^r'')
	// (this corresponds to one of the issued tokens)
}

type AuthorizationWitness struct {
	Sk      *bn256.Zr // issuer's secret key
	TType   *bn256.Zr // type the issuer is authorized to issue
	TNymBF  *bn256.Zr // randomness in Type
	Value   *bn256.Zr // Value in token
	TokenBF *bn256.Zr // randomness in token
	Index   int       // index of Type
}

type Signer struct {
	*Verifier
	Witness *AuthorizationWitness
}

type Verifier struct {
	PedersenParams []*bn256.G1
	Issuers        []*bn256.G1 // g_0^skg_1^type
	Auth           *Authorization
	BitLength      int
}

type Signature struct {
	AuthorizationCorrectness []byte
	TypeCorrectness          []byte
}

// check that the issuer knows the secret key of one of the commitments that link issuers to type
// check that type in the issued token is the same as type in the commitment NYM
func (s *Signer) Sign(message []byte) ([]byte, error) {
	if len(s.PedersenParams) != 3 {
		return nil, errors.Errorf("length of Pedersen parameters != 3")
	}

	// one out of many proofs
	commitments := make([]*bn256.G1, len(s.Issuers))
	for k, i := range s.Issuers {
		commitments[k] = bn256.NewG1()
		commitments[k].Copy(s.Auth.Type)
		commitments[k].Sub(i)
	}
	o2omp := o2omp.NewProver(commitments, message, []*bn256.G1{s.PedersenParams[0], s.PedersenParams[2]}, s.BitLength, s.Witness.Index, s.Witness.TNymBF)

	sig := &Signature{}
	var err error
	sig.AuthorizationCorrectness, err = o2omp.Prove()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to compute issuer's signature")
	}

	w := NewTypeCorrectnessWitness(s.Witness.Sk, s.Witness.TType, s.Witness.Value, s.Witness.TNymBF, s.Witness.TokenBF)

	tcp := NewTypeCorrectnessProver(w, s.Auth.Type, s.Auth.Token, message, s.PedersenParams)
	sig.TypeCorrectness, err = tcp.Prove()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to compute issuer's signature")
	}

	return sig.Serialize()
}

func (v *Verifier) Verify(message, rawsig []byte) error {
	if len(v.PedersenParams) != 3 {
		return errors.Errorf("length of Pedersen parameters != 3")
	}

	sig := &Signature{}
	err := sig.Deserialize(rawsig)
	if err != nil {
		return errors.Errorf("failed to unmarshal issuer's signature")
	}
	commitments := make([]*bn256.G1, len(v.Issuers))
	for k, i := range v.Issuers {
		commitments[k] = bn256.NewG1()
		commitments[k].Copy(v.Auth.Type)
		commitments[k].Sub(i)
	}

	// verify one out of many proof: issuer authorization
	err = o2omp.NewVerifier(commitments, message, []*bn256.G1{v.PedersenParams[0], v.PedersenParams[2]}, v.BitLength).Verify(sig.AuthorizationCorrectness)
	if err != nil {
		return errors.Wrapf(err, "failed to verify issuer's pseudonym")
	}

	// verify that type in authorization corresponds to type in token
	return NewTypeCorrectnessVerifier(v.Auth.Type, v.Auth.Token, message, v.PedersenParams).Verify(sig.TypeCorrectness)
}

func (s *Signature) Serialize() ([]byte, error) {
	return json.Marshal(s)
}

func (s *Signature) Deserialize(raw []byte) error {
	return json.Unmarshal(raw, s)
}

func NewWitness(sk, ttype, value, tNymBF, tokenBF *bn256.Zr, index int) *AuthorizationWitness {
	return &AuthorizationWitness{
		Sk:      sk,
		TType:   ttype,
		TNymBF:  tNymBF,
		Value:   value,
		TokenBF: tokenBF,
		Index:   index,
	}
}

// Initialize the prover
func NewSigner(witness *AuthorizationWitness, issuers []*bn256.G1, auth *Authorization, bitLength int, pp []*bn256.G1) *Signer {

	verifier := &Verifier{
		PedersenParams: pp,
		Issuers:        issuers,
		Auth:           auth,
		BitLength:      bitLength,
	}
	return &Signer{
		Witness:  witness,
		Verifier: verifier,
	}
}

func NewAuthorization(typeNym, token *bn256.G1) *Authorization {
	return &Authorization{
		Type:  typeNym,
		Token: token,
	}
}

func (v *Verifier) Serialize() ([]byte, error) {

	return json.Marshal(v)
}

func (v *Verifier) Deserialize(bitLength int, issuers, pp []*bn256.G1, token *bn256.G1, raw []byte) error {

	err := json.Unmarshal(raw, &v)
	if err != nil {
		return err
	}

	v.Auth.Token = token
	v.BitLength = bitLength
	v.PedersenParams = pp
	v.Issuers = issuers
	return nil
}

func (s *Signer) GetPublicVersion() api.Identity {
	return &Verifier{Auth: s.Auth, Issuers: s.Issuers, PedersenParams: s.PedersenParams, BitLength: s.BitLength}
}

func (s *Signer) ToUniqueIdentifier() ([]byte, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	logger.Debugf("ToUniqueIdentifier [%s]", string(raw))
	return []byte(hash.Hashable(raw).String()), nil
}
