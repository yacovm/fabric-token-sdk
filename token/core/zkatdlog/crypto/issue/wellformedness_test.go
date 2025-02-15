/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/
package issue_test

import (
	"github.com/hyperledger-labs/fabric-token-sdk/token/core/math/gurvy/bn256"
	"github.com/hyperledger-labs/fabric-token-sdk/token/core/zkatdlog/crypto/issue"
	"github.com/hyperledger-labs/fabric-token-sdk/token/core/zkatdlog/crypto/token"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Issued Token Correctness", func() {
	var (
		verifier *issue.WellFormednessVerifier
		prover   *issue.WellFormednessProver
	)
	BeforeEach(func() {
		prover = GetITCPProver()
		verifier = prover.WellFormednessVerifier
	})
	Describe("Prove", func() {
		Context("parameters and witness are initialized correctly", func() {
			It("Succeeds", func() {
				raw, err := prover.Prove()
				Expect(err).NotTo(HaveOccurred())
				Expect(raw).NotTo(BeNil())
				proof := &issue.WellFormedness{}
				err = proof.Deserialize(raw)
				Expect(err).NotTo(HaveOccurred())
				Expect(proof.Challenge).NotTo(BeNil())
				Expect(len(proof.BlindingFactors)).To(Equal(2))
				Expect(len(proof.Values)).To(Equal(2))
			})
		})
	})
	Describe("Verify", func() {
		It("Succeeds", func() {
			proof, err := prover.Prove()
			Expect(err).NotTo(HaveOccurred())
			err = verifier.Verify(proof)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})

func PrepareTokenWitness(pp []*bn256.G1) ([]*token.TokenDataWitness, []*bn256.G1, []*bn256.Zr) {
	rand, err := bn256.GetRand()
	Expect(err).NotTo(HaveOccurred())

	bF := make([]*bn256.Zr, 2)
	values := make([]*bn256.Zr, 2)
	for i := 0; i < 2; i++ {
		bF[i] = bn256.RandModOrder(rand)
	}
	ttype := "ABC"
	values[0] = bn256.NewZrInt(100)
	values[1] = bn256.NewZrInt(50)

	tokens := PrepareTokens(values, bF, ttype, pp)
	return issue.NewTokenDataWitness(ttype, values, bF), tokens, bF
}

func PrepareTokens(values, bf []*bn256.Zr, ttype string, pp []*bn256.G1) []*bn256.G1 {
	tokens := make([]*bn256.G1, len(values))
	for i := 0; i < len(values); i++ {
		tokens[i] = NewToken(values[i], bf[i], ttype, pp)
	}
	return tokens
}

func GetITCPProver() *issue.WellFormednessProver {
	pp := preparePedersenParameters()
	tw, tokens, _ := PrepareTokenWitness(pp)

	return issue.NewWellFormednessProver(tw, tokens, false, pp)
}

func preparePedersenParameters() []*bn256.G1 {
	rand, err := bn256.GetRand()
	Expect(err).NotTo(HaveOccurred())

	pp := make([]*bn256.G1, 3)

	for i := 0; i < 3; i++ {
		pp[i] = bn256.G1Gen().Mul(bn256.RandModOrder(rand))
	}
	return pp
}

func NewToken(value *bn256.Zr, rand *bn256.Zr, ttype string, pp []*bn256.G1) *bn256.G1 {
	token := bn256.NewG1()
	token.Add(pp[0].Mul(bn256.HashModOrder([]byte(ttype))))
	token.Add(pp[1].Mul(value))
	token.Add(pp[2].Mul(rand))
	return token
}
