/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/
package api

import (
	"bytes"
	"encoding/json"

	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	token2 "github.com/hyperledger-labs/fabric-token-sdk/token/token"
)

type TokenRequest struct {
	Issues           [][]byte
	Transfers        [][]byte
	Signatures       [][]byte
	AuditorSignature []byte
}

func (r *TokenRequest) Bytes() ([]byte, error) {
	return json.Marshal(r)
}

func (r *TokenRequest) FromBytes(raw []byte) error {
	return json.Unmarshal(raw, r)
}

type IssueMetadata struct {
	Issuer     view.Identity
	Outputs    [][]byte
	TokenInfo  [][]byte
	Receivers  []view.Identity
	AuditInfos [][]byte
}

// TransferMetadata contains the following information:
// - For each TokenID there is a sender
type TransferMetadata struct {
	TokenIDs           []*token2.Id
	Outputs            [][]byte
	TokenInfo          [][]byte
	Senders            []view.Identity
	SenderAuditInfos   [][]byte
	Receivers          []view.Identity
	ReceiverIsSender   []bool
	ReceiverAuditInfos [][]byte
}

type TokenRequestMetadata struct {
	Issues    []IssueMetadata
	Transfers []TransferMetadata
}

func (m *TokenRequestMetadata) TokenInfos() [][]byte {
	var res [][]byte
	for _, issue := range m.Issues {
		res = append(res, issue.TokenInfo...)
	}
	for _, transfer := range m.Transfers {
		res = append(res, transfer.TokenInfo...)
	}
	return res
}

func (m *TokenRequestMetadata) GetTokenInfo(tokenRaw []byte) []byte {
	for _, issue := range m.Issues {
		for i, output := range issue.Outputs {
			if bytes.Equal(output, tokenRaw) {
				return issue.TokenInfo[i]
			}
		}
	}
	for _, transfer := range m.Transfers {
		for i, output := range transfer.Outputs {
			if bytes.Equal(output, tokenRaw) {
				return transfer.TokenInfo[i]
			}
		}
	}
	return nil
}

func (m *TokenRequestMetadata) Recipients() [][]byte {
	var res [][]byte
	for _, issue := range m.Issues {
		for _, r := range issue.Receivers {
			res = append(res, r.Bytes())
		}
	}
	for _, transfer := range m.Transfers {
		for _, r := range transfer.Receivers {
			res = append(res, r.Bytes())
		}
	}
	return res
}

func (m *TokenRequestMetadata) Senders() [][]byte {
	var res [][]byte
	for _, transfer := range m.Transfers {
		for _, s := range transfer.Senders {
			res = append(res, s.Bytes())
		}
	}
	return res
}

func (m *TokenRequestMetadata) Issuers() [][]byte {
	var res [][]byte
	for _, issue := range m.Issues {
		res = append(res, issue.Issuer)
	}
	return res
}

func (m *TokenRequestMetadata) Inputs() []*token2.Id {
	var res []*token2.Id
	for _, transfer := range m.Transfers {
		res = append(res, transfer.TokenIDs...)
	}
	return res
}

func (m *TokenRequestMetadata) Bytes() ([]byte, error) {
	return json.Marshal(m)
}

func (m *TokenRequestMetadata) FromBytes(raw []byte) error {
	return json.Unmarshal(raw, m)
}
