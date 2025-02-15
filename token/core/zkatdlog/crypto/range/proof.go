/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/
package rangeproof

import (
	"crypto/sha256"
	"encoding/json"
	"math"

	"github.com/hyperledger-labs/fabric-token-sdk/token/core/math/gurvy/bn256"
	"github.com/hyperledger-labs/fabric-token-sdk/token/core/zkatdlog/crypto/common"
	"github.com/hyperledger-labs/fabric-token-sdk/token/core/zkatdlog/crypto/pssign"
	"github.com/hyperledger-labs/fabric-token-sdk/token/core/zkatdlog/crypto/sigproof"
	"github.com/hyperledger-labs/fabric-token-sdk/token/core/zkatdlog/crypto/token"
	"github.com/pkg/errors"
)

// todo check lengths
type Proof struct {
	Challenge        *bn256.Zr
	EqualityProofs   *EqualityProofs
	MembershipProofs []*MembershipProof
}

type EqualityProofs struct {
	Type                     *bn256.Zr
	Value                    []*bn256.Zr
	TokenBlindingFactor      []*bn256.Zr
	CommitmentBlindingFactor []*bn256.Zr
}

type MembershipProof struct {
	Commitments     []*bn256.G1
	SignatureProofs [][]byte
}

type Prover struct {
	*Verifier
	tokenWitness             []*token.TokenDataWitness
	membershipWitness        [][]*sigproof.MembershipWitness
	commitmentBlindingFactor []*bn256.Zr
	Signatures               []*pssign.Signature
	randomness               *Randomness
	Commitment               *Commitment
}

func NewProver(tw []*token.TokenDataWitness, token []*bn256.G1, signatures []*pssign.Signature, exponent int, pp []*bn256.G1, PK []*bn256.G2, P *bn256.G1, Q *bn256.G2) *Prover {
	return &Prover{
		tokenWitness: tw,
		Signatures:   signatures,
		Verifier: &Verifier{
			Token:          token,
			Base:           uint64(len(signatures)),
			Exponent:       exponent,
			PedersenParams: pp,
			PK:             PK,
			P:              P,
			Q:              Q,
		},
	}
}

type Verifier struct {
	Token          []*bn256.G1
	Base           uint64
	Exponent       int
	PedersenParams []*bn256.G1
	Q              *bn256.G2
	P              *bn256.G1
	PK             []*bn256.G2
}

func NewVerifier(token []*bn256.G1, base uint64, exponent int, pp []*bn256.G1, PK []*bn256.G2, P *bn256.G1, Q *bn256.G2) *Verifier {
	return &Verifier{
		Token:          token,
		Base:           base,
		Exponent:       exponent,
		PedersenParams: pp,
		PK:             PK,
		P:              P,
		Q:              Q,
	}
}

type Randomness struct {
	Type                     *bn256.Zr
	Value                    []*bn256.Zr
	TokenBlindingFactor      []*bn256.Zr
	CommitmentBlindingFactor []*bn256.Zr
}

type Commitment struct {
	Token             []*bn256.G1
	CommitmentToValue []*bn256.G1
}

func (p *Prover) Prove() ([]byte, error) {
	proof := &Proof{}
	var err error
	coms, err := p.computeMembershipWitness()
	if err != nil {
		return nil, err
	}

	proof.MembershipProofs = make([]*MembershipProof, len(p.Token))
	for k := 0; k < len(proof.MembershipProofs); k++ {
		proof.MembershipProofs[k] = &MembershipProof{}
		proof.MembershipProofs[k].Commitments = make([]*bn256.G1, p.Exponent)
		proof.MembershipProofs[k].SignatureProofs = make([][]byte, p.Exponent)
		for i := 0; i < p.Exponent; i++ {
			proof.MembershipProofs[k].Commitments[i] = coms[k][i]
			mp := sigproof.NewMembershipProver(p.membershipWitness[k][i], proof.MembershipProofs[k].Commitments[i], p.P, p.Q, p.PK, p.PedersenParams[:2])
			proof.MembershipProofs[k].SignatureProofs[i], err = mp.Prove()
			if err != nil {
				return nil, err
			}
		}
	}
	// show that value in token = value in the aggregate commitment
	err = p.computeCommitment()
	if err != nil {
		return nil, err
	}

	proof.Challenge = p.computeChallenge(p.Commitment, coms)

	// equality proof
	proof.EqualityProofs = &EqualityProofs{}
	for k := 0; k < len(p.Token); k++ {
		sp := &common.SchnorrProver{Challenge: proof.Challenge, Randomness: []*bn256.Zr{p.randomness.Value[k], p.randomness.TokenBlindingFactor[k], p.randomness.CommitmentBlindingFactor[k]}, Witness: []*bn256.Zr{p.tokenWitness[k].Value, p.tokenWitness[k].BlindingFactor, p.commitmentBlindingFactor[k]}}
		proofs, err := sp.Prove()
		if err != nil {
			return nil, err
		}
		proof.EqualityProofs.Value = append(proof.EqualityProofs.Value, proofs[0])
		proof.EqualityProofs.TokenBlindingFactor = append(proof.EqualityProofs.TokenBlindingFactor, proofs[1])
		proof.EqualityProofs.CommitmentBlindingFactor = append(proof.EqualityProofs.CommitmentBlindingFactor, proofs[2])
	}
	proof.EqualityProofs.Type = bn256.ModMul(proof.Challenge, bn256.HashModOrder([]byte(p.tokenWitness[0].Type)), bn256.Order)
	proof.EqualityProofs.Type = bn256.ModAdd(proof.EqualityProofs.Type, p.randomness.Type, bn256.Order)

	return json.Marshal(proof)
}

func (v *Verifier) Verify(raw []byte) error {

	proof := &Proof{}
	err := json.Unmarshal(raw, proof)
	if err != nil {
		return err
	}
	if len(proof.MembershipProofs) != len(v.Token) {
		return errors.Errorf("failed to verify range proofz")
	}
	//  verify membership
	for k := 0; k < len(v.Token); k++ {
		if len(proof.MembershipProofs[k].Commitments) != len(proof.MembershipProofs[k].SignatureProofs) {
			return errors.Errorf("failed to verify range proof")
		}
		for i := 0; i < len(proof.MembershipProofs[k].Commitments); i++ {
			mv := sigproof.NewMembershipVerifier(proof.MembershipProofs[k].Commitments[i], v.P, v.Q, v.PK, v.PedersenParams[:2])
			err = mv.Verify(proof.MembershipProofs[k].SignatureProofs[i])
			if err != nil {
				return errors.Wrapf(err, "failed to verify range proof")
			}
		}
	}

	//  verify equality
	com := v.recomputeCommitments(proof)
	coms := make([][]*bn256.G1, len(proof.MembershipProofs))
	for i := 0; i < len(proof.MembershipProofs); i++ {
		for k := 0; k < len(proof.MembershipProofs[i].Commitments); k++ {
			coms[i] = append(coms[i], proof.MembershipProofs[i].Commitments[k])
		}
	}
	chal := v.computeChallenge(com, coms)
	if chal.Cmp(proof.Challenge) != 0 {
		return errors.Errorf("failed to verify range proof")
	}

	return nil
}
func (p *Prover) computeMembershipWitness() ([][]*bn256.G1, error) {
	rand, err := bn256.GetRand()
	if err != nil {
		return nil, err
	}
	p.membershipWitness = make([][]*sigproof.MembershipWitness, len(p.tokenWitness))
	p.commitmentBlindingFactor = make([]*bn256.Zr, len(p.tokenWitness))
	coms := make([][]*bn256.G1, len(p.tokenWitness))

	for k := 0; k < len(p.tokenWitness); k++ {
		values := make([]int, p.Exponent)
		v := p.tokenWitness[k].Value.Int64()
		if v >= int64(math.Pow(float64(p.Base), float64(p.Exponent))) {
			return nil, errors.Errorf("can't compute range proof: value of token outside authorized range")
		}
		values[0] = int(v % int64(p.Base))
		for i := 0; i < p.Exponent-1; i++ {
			values[p.Exponent-1-i] = int(v / int64(math.Pow(float64(p.Base), float64(p.Exponent-1-i)))) // quotient
			v = v % int64(math.Pow(float64(p.Base), float64(p.Exponent-1-i)))                           // remainder
		}

		p.membershipWitness[k] = make([]*sigproof.MembershipWitness, p.Exponent)
		p.commitmentBlindingFactor[k] = bn256.NewZr()
		coms[k] = make([]*bn256.G1, p.Exponent)
		for i := 0; i < p.Exponent; i++ {
			bf := bn256.RandModOrder(rand)
			coms[k][i], err = common.ComputePedersenCommitment([]*bn256.Zr{bn256.NewZrInt(values[i]), bf}, p.PedersenParams[:2])
			if err != nil {
				return nil, err
			}

			p.membershipWitness[k][i] = sigproof.NewMembershipWitness(p.Signatures[values[i]], bn256.NewZrInt(values[i]), bf)
			pow := bn256.NewZrInt(int(math.Pow(float64(p.Base), float64(i))))
			p.commitmentBlindingFactor[k] = bn256.ModAdd(p.commitmentBlindingFactor[k], bn256.ModMul(bf, pow, bn256.Order), bn256.Order)
		}
	}
	return coms, nil
}

func (p *Prover) computeCommitment() error {
	rand, err := bn256.GetRand()
	if err != nil {
		return err
	}
	// generate randomness
	p.randomness = &Randomness{}
	p.randomness.Type = bn256.RandModOrder(rand)
	for i := 0; i < len(p.Token); i++ {
		p.randomness.Value = append(p.randomness.Value, bn256.RandModOrder(rand))
		p.randomness.CommitmentBlindingFactor = append(p.randomness.CommitmentBlindingFactor, bn256.RandModOrder(rand))
		p.randomness.TokenBlindingFactor = append(p.randomness.TokenBlindingFactor, bn256.RandModOrder(rand))
	}

	// compute commitment
	p.Commitment = &Commitment{}
	for i := 0; i < len(p.tokenWitness); i++ {
		tok := p.PedersenParams[0].Mul(p.randomness.Type)
		tok.Add(p.PedersenParams[1].Mul(p.randomness.Value[i]))
		tok.Add(p.PedersenParams[2].Mul(p.randomness.TokenBlindingFactor[i]))
		p.Commitment.Token = append(p.Commitment.Token, tok)

		com := p.PedersenParams[0].Mul(p.randomness.Value[i])
		com.Add(p.PedersenParams[1].Mul(p.randomness.CommitmentBlindingFactor[i]))
		p.Commitment.CommitmentToValue = append(p.Commitment.CommitmentToValue, com)
	}

	return nil

}

func (v *Verifier) computeChallenge(commitment *Commitment, comToValue [][]*bn256.G1) *bn256.Zr {
	g1array := common.GetG1Array([]*bn256.G1{v.P}, v.Token, commitment.Token, commitment.CommitmentToValue, v.PedersenParams)
	g2array := common.GetG2Array([]*bn256.G2{v.Q}, v.PK)
	bytes := append(g1array.Bytes(), g2array.Bytes()...)
	for i := 0; i < len(comToValue); i++ {
		bytes = append(bytes, common.GetG1Array(comToValue[i]).Bytes()...)
	}
	hash := sha256.Sum256(bytes)
	return bn256.NewZrFromBytes(hash[:])
}

func (v *Verifier) recomputeCommitments(p *Proof) *Commitment {
	c := &Commitment{}
	// recompute commitments for verification
	for j := 0; j < len(v.Token); j++ {
		ver := &common.SchnorrVerifier{PedParams: v.PedersenParams}
		zkp := &common.SchnorrProof{Statement: v.Token[j], Proof: []*bn256.Zr{p.EqualityProofs.Type, p.EqualityProofs.Value[j], p.EqualityProofs.TokenBlindingFactor[j]}, Challenge: p.Challenge}
		c.Token = append(c.Token, ver.RecomputeCommitment(zkp))
	}

	for j := 0; j < len(v.Token); j++ {
		com := bn256.NewG1()
		for i := 0; i < v.Exponent; i++ {
			pow := bn256.NewZrInt(int(math.Pow(float64(v.Base), float64(i))))
			com.Add(p.MembershipProofs[j].Commitments[i].Mul(pow))
		}

		ver := &common.SchnorrVerifier{PedParams: v.PedersenParams[:2]}
		zkp := &common.SchnorrProof{Statement: com, Proof: []*bn256.Zr{p.EqualityProofs.Value[j], p.EqualityProofs.CommitmentBlindingFactor[j]}, Challenge: p.Challenge}
		c.CommitmentToValue = append(c.CommitmentToValue, ver.RecomputeCommitment(zkp))
	}
	return c

}
