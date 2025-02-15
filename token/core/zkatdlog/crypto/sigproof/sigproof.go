/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/
package sigproof

import (
	"github.com/hyperledger-labs/fabric-token-sdk/token/core/math/gurvy/bn256"
	"github.com/hyperledger-labs/fabric-token-sdk/token/core/zkatdlog/crypto/common"
	"github.com/hyperledger-labs/fabric-token-sdk/token/core/zkatdlog/crypto/pssign"
	"github.com/pkg/errors"
)

type SigProof struct {
	Challenge         *bn256.Zr
	Hidden            []*bn256.Zr
	Hash              *bn256.Zr
	Signature         *pssign.Signature
	SigBlindingFactor *bn256.Zr
	ComBlindingFactor *bn256.Zr
	Commitment        *bn256.G1 // for hidden values
}

// commitments how they are computed
type SigProver struct {
	*SigVerifier
	witness    *SigWitness
	randomness *SigRandomness
	Commitment *SigCommitment
}

type SigVerifier struct {
	*POKVerifier
	HiddenIndices        []int
	DisclosedIndices     []int
	Disclosed            []*bn256.Zr
	PedersenParams       []*bn256.G1
	CommitmentToMessages *bn256.G1
}

type SigWitness struct {
	hidden            []*bn256.Zr
	hash              *bn256.Zr
	signature         *pssign.Signature
	sigBlindingFactor *bn256.Zr
	comBlindingFactor *bn256.Zr
}

func NewSigWitness(hidden []*bn256.Zr, signature *pssign.Signature, hash, bf *bn256.Zr) *SigWitness {
	return &SigWitness{
		hidden:            hidden,
		hash:              hash,
		signature:         signature,
		comBlindingFactor: bf,
	}
}

func NewSigProver(hidden, disclosed []*bn256.Zr, signature *pssign.Signature, hash, bf *bn256.Zr, com *bn256.G1, hiddenindices, disclosedindices []int, P *bn256.G1, Q *bn256.G2, PK []*bn256.G2, pp []*bn256.G1) *SigProver {
	return &SigProver{
		witness:     NewSigWitness(hidden, signature, hash, bf),
		SigVerifier: NewSigVerifier(hiddenindices, disclosedindices, disclosed, com, P, Q, PK, pp),
	}
}

func NewSigVerifier(hidden, disclosed []int, disclosedInf []*bn256.Zr, com, P *bn256.G1, Q *bn256.G2, PK []*bn256.G2, pp []*bn256.G1) *SigVerifier {
	return &SigVerifier{
		POKVerifier: &POKVerifier{
			P:  P,
			Q:  Q,
			PK: PK,
		},
		HiddenIndices:        hidden,
		DisclosedIndices:     disclosed,
		Disclosed:            disclosedInf,
		CommitmentToMessages: com,
		PedersenParams:       pp,
	}
}

type SigRandomness struct {
	hidden            []*bn256.Zr
	hash              *bn256.Zr
	sigBlindingFactor *bn256.Zr
	comBlindingFactor *bn256.Zr
}

type SigCommitment struct {
	CommitmentToMessages *bn256.G1
	Signature            *bn256.GT
}

func (p *SigProver) Prove() (*SigProof, error) {
	if len(p.HiddenIndices) != len(p.witness.hidden) {
		return nil, errors.Errorf("witness is not of the right size")
	}
	if len(p.DisclosedIndices) != len(p.Disclosed) {
		return nil, errors.Errorf("witness is not of the right size")
	}
	proof := &SigProof{}
	var err error
	// randomize signature
	proof.Signature, err = p.obfuscateSignature()
	if err != nil {
		return nil, err
	}
	err = p.computeCommitment()
	if err != nil {
		return nil, err
	}

	// compute challenge
	proof.Challenge, err = p.computeChallenge(p.CommitmentToMessages, proof.Signature, p.Commitment)
	if err != nil {
		return nil, err
	}
	// compute proofs
	sp := &common.SchnorrProver{Witness: append(p.witness.hidden, p.witness.comBlindingFactor, p.witness.sigBlindingFactor, p.witness.hash), Randomness: append(p.randomness.hidden, p.randomness.comBlindingFactor, p.randomness.sigBlindingFactor, p.randomness.hash), Challenge: proof.Challenge}
	proofs, err := sp.Prove()
	if err != nil {
		return nil, errors.Wrapf(err, "signature proof generation failed")
	}

	proof.Commitment = p.CommitmentToMessages
	proof.Hidden = proofs[:len(p.witness.hidden)]
	proof.ComBlindingFactor = proofs[len(p.witness.hidden)]
	proof.SigBlindingFactor = proofs[len(p.witness.hidden)+1]
	proof.Hash = proofs[len(p.witness.hidden)+2]

	return proof, nil
}

func (p *SigProver) computeCommitment() error {
	if len(p.PedersenParams) != len(p.HiddenIndices)+1 {
		return errors.Errorf("size of witness does not match length of Pedersen Parameters")
	}
	if len(p.PK) != len(p.HiddenIndices)+len(p.DisclosedIndices)+2 {
		return errors.Errorf("size of signature public key does not mathc the size of the witness")
	}
	// Get RNG
	rand, err := bn256.GetRand()
	if err != nil {
		return errors.Errorf("failed to get RNG")
	}
	// generate randomness
	p.randomness = &SigRandomness{}
	for i := 0; i < len(p.witness.hidden); i++ {
		p.randomness.hidden = append(p.randomness.hidden, bn256.RandModOrder(rand))
	}
	p.randomness.hash = bn256.RandModOrder(rand)
	p.randomness.comBlindingFactor = bn256.RandModOrder(rand)
	p.randomness.sigBlindingFactor = bn256.RandModOrder(rand)

	// compute commitment
	p.Commitment = &SigCommitment{}
	p.Commitment.CommitmentToMessages = p.PedersenParams[len(p.witness.hidden)].Mul(p.randomness.comBlindingFactor)
	for i, r := range p.randomness.hidden {
		p.Commitment.CommitmentToMessages.Add(p.PedersenParams[i].Mul(r))
	}

	t := p.PK[len(p.Disclosed)+len(p.witness.hidden)+1].Mul(p.randomness.hash)
	for i, index := range p.HiddenIndices {
		t.Add(p.PK[index+1].Mul(p.randomness.hidden[i]))
	}

	p.Commitment.Signature = bn256.Pairing(t, p.witness.signature.R, p.Q, p.P.Mul(p.randomness.sigBlindingFactor))
	p.Commitment.Signature = bn256.FinalExp(p.Commitment.Signature)
	return nil
}

func (p *SigProver) obfuscateSignature() (*pssign.Signature, error) {
	rand, err := bn256.GetRand()
	if err != nil {
		return nil, errors.Errorf("failed to get RNG")
	}

	p.witness.sigBlindingFactor = bn256.RandModOrder(rand)
	err = p.witness.signature.Randomize()
	if err != nil {
		return nil, err
	}
	sig := &pssign.Signature{}
	sig.Copy(p.witness.signature)
	sig.S.Add(p.P.Mul(p.witness.sigBlindingFactor))

	return sig, nil
}

func (v *SigVerifier) computeChallenge(comToMessages *bn256.G1, signature *pssign.Signature, com *SigCommitment) (*bn256.Zr, error) {
	g1array := common.GetG1Array(v.PedersenParams, []*bn256.G1{comToMessages, com.CommitmentToMessages,
		v.P})
	g2array := common.GetG2Array(v.PK, []*bn256.G2{v.Q})
	raw := common.GetBytesArray(g1array.Bytes(), g2array.Bytes(), com.Signature.Bytes())
	bytes, err := signature.Serialize()
	if err != nil {
		return nil, errors.Errorf("failed to compute challenge: error while serializing Pointcheval-Sanders signature")
	}
	raw = append(raw, bytes...)

	return bn256.HashModOrder(raw), nil
}

// recompute commitments for verification
func (v *SigVerifier) recomputeCommitments(p *SigProof) (*SigCommitment, error) {
	if len(p.Hidden)+len(v.Disclosed) != len(v.PK)-2 {
		return nil, errors.Errorf("length of signature public key does not match number of signed messages")
	}

	c := &SigCommitment{}
	ver := &common.SchnorrVerifier{PedParams: v.PedersenParams}
	zkp := &common.SchnorrProof{Statement: v.CommitmentToMessages, Proof: append(p.Hidden, p.ComBlindingFactor), Challenge: p.Challenge}

	c.CommitmentToMessages = ver.RecomputeCommitment(zkp)
	proof := make([]*bn256.Zr, len(v.PK)-2)
	for i, index := range v.HiddenIndices {
		proof[index] = p.Hidden[i]
	}
	for i, index := range v.DisclosedIndices {
		proof[index] = bn256.ModMul(v.Disclosed[i], p.Challenge, bn256.Order)
	}

	sp := &POK{
		Challenge:      p.Challenge,
		Signature:      p.Signature,
		Messages:       proof,
		Hash:           p.Hash,
		BlindingFactor: p.SigBlindingFactor,
	}

	sv := &POKVerifier{P: v.P, Q: v.Q, PK: v.PK}
	var err error
	c.Signature, err = sv.RecomputeCommitment(sp)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to verify signature proof")
	}
	return c, nil
}

// verify membership proof
func (v *SigVerifier) Verify(p *SigProof) error {

	com, err := v.recomputeCommitments(p)
	if err != nil {
		return nil
	}

	chal, err := v.computeChallenge(p.Commitment, p.Signature, com)
	if err != nil {
		return nil
	}
	if chal.Cmp(p.Challenge) != 0 {
		return errors.Errorf("invalid signature proof")
	}
	return nil
}
