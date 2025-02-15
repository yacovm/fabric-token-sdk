/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/
package token

import (
	"encoding/json"

	"github.com/pkg/errors"

	view2 "github.com/hyperledger-labs/fabric-smart-client/platform/view"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"

	api2 "github.com/hyperledger-labs/fabric-token-sdk/token/api"
	token2 "github.com/hyperledger-labs/fabric-token-sdk/token/token"
)

type TransferOptions struct {
	Selector Selector
	TokenIDs []*token2.Id
}

func compileTransferOptions(opts ...TransferOption) (*TransferOptions, error) {
	txOptions := &TransferOptions{}
	for _, opt := range opts {
		if err := opt(txOptions); err != nil {
			return nil, err
		}
	}
	return txOptions, nil
}

type TransferOption func(*TransferOptions) error

func WithTokenSelector(selector Selector) TransferOption {
	return func(o *TransferOptions) error {
		o.Selector = selector
		return nil
	}
}

func WithTokenIDs(ids ...*token2.Id) TransferOption {
	return func(o *TransferOptions) error {
		o.TokenIDs = ids
		return nil
	}
}

type AuditRecord struct {
	TxID   string
	Inputs *InputStream
	Ouputs *OutputStream
}

type Issue struct {
	Issuer    view.Identity
	Receivers []view.Identity
}

type Transfer struct {
	Senders   []view.Identity
	Receivers []view.Identity
}

type Request struct {
	TxID         string
	Actions      *api2.TokenRequest
	Metadata     *api2.TokenRequestMetadata
	TokenService *ManagementService `json:"-"`
}

func NewRequest(tokenService *ManagementService, txid string) *Request {
	return &Request{
		TxID:         txid,
		Actions:      &api2.TokenRequest{},
		Metadata:     &api2.TokenRequestMetadata{},
		TokenService: tokenService,
	}
}

func NewRequestFromBytes(tokenService *ManagementService, txid string, trRaw []byte, trmRaw []byte) (*Request, error) {
	tr := &api2.TokenRequest{}
	if err := tr.FromBytes(trRaw); err != nil {
		return nil, errors.Wrapf(err, "failed unmarshalling token request [%d]", len(trRaw))
	}
	trm := &api2.TokenRequestMetadata{}
	if len(trmRaw) != 0 {
		if err := trm.FromBytes(trmRaw); err != nil {
			return nil, errors.Wrapf(err, "failed unmarshalling token request metadata [%d]", len(trmRaw))
		}
	}
	return &Request{
		TxID:         txid,
		Actions:      tr,
		Metadata:     trm,
		TokenService: tokenService,
	}, nil
}

func (t *Request) ID() string {
	return t.TxID
}

func (t *Request) Issue(wallet *IssuerWallet, receiver view.Identity, typ string, q uint64) (*IssueAction, error) {
	if receiver.IsNone() {
		return nil, errors.Errorf("all recipients should be defined")
	}

	id, err := wallet.GetIssuerIdentity(typ)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed getting issuer identity for type [%s]", typ)
	}

	// Compute Issue
	issue, tokenInfos, issuer, err := t.TokenService.tms.Issue(id, typ, []uint64{q}, [][]byte{receiver})
	if err != nil {
		return nil, err
	}

	// Append
	raw, err := issue.Serialize()
	if err != nil {
		return nil, err
	}

	t.Actions.Issues = append(t.Actions.Issues, raw)
	outputs, err := issue.GetSerializedOutputs()
	if err != nil {
		return nil, err
	}

	auditInfo, err := t.TokenService.tms.GetAuditInfo(receiver)
	if err != nil {
		return nil, err
	}

	t.Metadata.Issues = append(t.Metadata.Issues,
		api2.IssueMetadata{
			Issuer:     issuer,
			Outputs:    outputs,
			TokenInfo:  tokenInfos,
			Receivers:  []view.Identity{receiver},
			AuditInfos: [][]byte{auditInfo},
		},
	)

	return &IssueAction{a: issue}, nil
}

func (t *Request) Transfer(wallet *OwnerWallet, typ string, values []uint64, owners []view.Identity, opts ...TransferOption) (*TransferAction, error) {
	tokenIDs, outputTokens, err := t.prepareTransfer(false, wallet, typ, values, owners, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "failed preparing transfer")
	}

	logger.Debugf("Prepare Transfer Action [id:%s,ins:%d,outs:%d]", t.TxID, len(tokenIDs), len(outputTokens))

	ts := t.TokenService.tms

	// Compute transfer
	transfer, transferMetadata, err := ts.Transfer(t.TxID, wallet.w, tokenIDs, outputTokens...)
	if err != nil {
		return nil, errors.Wrap(err, "failed creating transfer action")
	}
	// double check
	if err := ts.VerifyTransfer(transfer, transferMetadata.TokenInfo); err != nil {
		return nil, errors.Wrap(err, "failed checking generated proof")
	}

	// Append
	raw, err := transfer.Serialize()
	if err != nil {
		return nil, errors.Wrap(err, "failed serializing transfer action")
	}
	t.Actions.Transfers = append(t.Actions.Transfers, raw)
	t.Metadata.Transfers = append(t.Metadata.Transfers, *transferMetadata)

	return &TransferAction{a: transfer}, nil
}

func (t *Request) Redeem(wallet *OwnerWallet, typ string, value uint64, opts ...TransferOption) error {
	tokenIDs, outputTokens, err := t.prepareTransfer(true, wallet, typ, []uint64{value}, []view.Identity{nil}, opts...)
	if err != nil {
		return errors.Wrap(err, "failed preparing transfer")
	}

	logger.Debugf("Prepare Redeem Action [ins:%d,outs:%d]", len(tokenIDs), len(outputTokens))

	ts := t.TokenService.tms

	// Compute redeem, it is a transfer with owner set to nil
	transfer, transferMetadata, err := ts.Transfer(t.TxID, wallet.w, tokenIDs, outputTokens...)
	if err != nil {
		return errors.Wrap(err, "failed creating transfer action")
	}

	// double check
	if err := ts.VerifyTransfer(transfer, transferMetadata.TokenInfo); err != nil {
		return errors.Wrap(err, "failed checking generated proof")
	}

	// Append
	raw, err := transfer.Serialize()
	if err != nil {
		return errors.Wrap(err, "failed serializing transfer action")
	}
	t.Actions.Transfers = append(t.Actions.Transfers, raw)
	t.Metadata.Transfers = append(t.Metadata.Transfers, *transferMetadata)

	return nil
}

func (t *Request) Outputs() (*OutputStream, error) {
	var outputs []*Output
	for i, issue := range t.Actions.Issues {
		action, err := t.TokenService.tms.DeserializeIssueAction(issue)
		if err != nil {
			return nil, errors.Wrapf(err, "failed deserializing issue action [%d]", i)
		}
		for j, output := range action.GetOutputs() {
			raw, err := output.Serialize()
			if err != nil {
				return nil, errors.Wrapf(err, "failed deserializing issue action output [%d,%d]", i, j)
			}
			tok, _, err := t.TokenService.tms.DeserializeToken(raw, t.Metadata.Issues[i].TokenInfo[j])
			if err != nil {
				return nil, errors.Wrapf(err, "failed getting issue action output in the clear [%d,%d]", i, j)
			}
			eID, err := t.TokenService.tms.GetEnrollmentID(t.Metadata.Issues[i].AuditInfos[j])
			if err != nil {
				return nil, errors.Wrapf(err, "failed getting enrollment id [%d,%d]", i, j)
			}

			outputs = append(outputs, &Output{
				ActionIndex:  i,
				Owner:        tok.Owner.Raw,
				EnrollmentID: eID,
				Type:         tok.Type,
				Quantity:     tok.Quantity,
			})
		}
	}
	for i, transfer := range t.Actions.Transfers {
		action, err := t.TokenService.tms.DeserializeTransferAction(transfer)
		if err != nil {
			return nil, errors.Wrapf(err, "failed deserializing transfer action [%d]", i)
		}
		for j, output := range action.GetOutputs() {
			raw, err := output.Serialize()
			if err != nil {
				return nil, errors.Wrapf(err, "failed deserializing transfer action output [%d,%d]", i, j)
			}
			tok, _, err := t.TokenService.tms.DeserializeToken(raw, t.Metadata.Transfers[i].TokenInfo[j])
			if err != nil {
				return nil, errors.Wrapf(err, "failed getting transfer action output in the clear [%d,%d]", i, j)
			}
			var eID string
			if len(tok.Owner.Raw) != 0 {
				eID, err = t.TokenService.tms.GetEnrollmentID(t.Metadata.Transfers[i].ReceiverAuditInfos[j])
				if err != nil {
					return nil, errors.Wrapf(err, "failed getting enrollment id [%d,%d]", i, j)
				}
			}

			outputs = append(outputs, &Output{
				ActionIndex:  i,
				Owner:        tok.Owner.Raw,
				EnrollmentID: eID,
				Type:         tok.Type,
				Quantity:     tok.Quantity,
			})
		}
	}

	return NewOutputStream(outputs), nil
}

func (t *Request) Inputs() (*InputStream, error) {
	var inputs []*Input
	for i := range t.Actions.Transfers {
		meta := t.Metadata.Transfers[i]

		for j, id := range meta.TokenIDs {
			eID, err := t.TokenService.tms.GetEnrollmentID(t.Metadata.Transfers[i].SenderAuditInfos[j])
			if err != nil {
				return nil, errors.Wrapf(err, "failed getting enrollment id [%d,%d]", i, j)
			}
			inputs = append(inputs, &Input{
				ActionIndex:  i,
				Id:           id,
				Owner:        meta.Senders[j],
				EnrollmentID: eID,
			})
		}
	}
	return NewInputStream(t.TokenService.Vault().NewQueryEngine(), inputs), nil
}

func (t *Request) Verify() error {
	ts := t.TokenService.tms
	for i, issue := range t.Actions.Issues {
		action, err := ts.DeserializeIssueAction(issue)
		if err != nil {
			return errors.WithMessagef(err, "failed deserializing issue action")
		}
		if err := ts.VerifyIssue(action, t.Metadata.Issues[i].TokenInfo); err != nil {
			return errors.WithMessagef(err, "failed verifying issue action")
		}
	}
	for i, transfer := range t.Actions.Transfers {
		action, err := ts.DeserializeTransferAction(transfer)
		if err != nil {
			return errors.WithMessagef(err, "failed deserializing transfer action")
		}
		if err := ts.VerifyTransfer(action, t.Metadata.Transfers[i].TokenInfo); err != nil {
			return errors.WithMessagef(err, "failed verifying transfer action")
		}
	}

	if _, err := t.Inputs(); err != nil {
		return errors.WithMessagef(err, "failed verifying inputs")
	}

	if _, err := t.Outputs(); err != nil {
		return errors.WithMessagef(err, "failed verifying outputs")
	}
	return nil
}

func (t *Request) IsValid() error {
	// TODO: IsValid tokens
	numTokens, err := t.countOutputs()
	if err != nil {
		return errors.Wrapf(err, "failed extracting tokens")
	}
	tis := t.Metadata.TokenInfos()
	if numTokens != len(tis) {
		return errors.Errorf("invalid transaction, the number of tokens differs from the number of token info [%d],[%d]", numTokens, len(tis))
	}

	return t.Verify()
}

func (t *Request) MarshallToAudit() ([]byte, error) {
	bytes, err := json.Marshal(&api2.TokenRequest{Issues: t.Actions.Issues, Transfers: t.Actions.Transfers})
	if err != nil {
		return nil, errors.Wrapf(err, "audit of tx [%s] failed: error marshal token request for signature", t.TxID)
	}
	return append(bytes, []byte(t.TxID)...), nil
}

func (t *Request) MarshallToSign() ([]byte, error) {
	req := &api2.TokenRequest{
		Issues:    t.Actions.Issues,
		Transfers: t.Actions.Transfers,
	}
	return json.Marshal(req)
}

func (t *Request) RequestToBytes() ([]byte, error) {
	return t.Actions.Bytes()
}

func (t *Request) MetadataToBytes() ([]byte, error) {
	return t.Metadata.Bytes()
}

func (t *Request) SetAuditorSignature(sigma []byte) {
	t.Actions.AuditorSignature = sigma
}

func (t *Request) AppendSignature(sigma []byte) {
	t.Actions.Signatures = append(t.Actions.Signatures, sigma)
}

func (t *Request) SetTokenService(service *ManagementService) {
	t.TokenService = service
}

// BindTo binds transfers' senders and receivers, that are senders, that are not me to the passed identity
func (t *Request) BindTo(sp view2.ServiceProvider, party view.Identity) error {
	resolver := view2.GetEndpointService(sp)
	longTermIdentity, _, _, err := view2.GetEndpointService(sp).Resolve(party)
	if err != nil {
		return errors.Wrap(err, "cannot resolve identity")
	}

	for i := range t.Actions.Transfers {
		for _, eid := range t.Metadata.Transfers[i].Senders {
			if w := t.TokenService.WalletManager().Wallet(eid); w != nil {
				// this is me, skip
				continue
			}
			logger.Debugf("bind sender [%s] to [%s]", eid, party)
			if err := resolver.Bind(longTermIdentity, eid); err != nil {
				return errors.Wrap(err, "failed binding sender identities")
			}
		}
		receivers := t.Metadata.Transfers[i].Receivers
		for j, b := range t.Metadata.Transfers[i].ReceiverIsSender {
			if b {
				if w := t.TokenService.WalletManager().Wallet(receivers[j]); w != nil {
					// this is me, skip
					continue
				}

				logger.Debugf("bind receiver as sender [%s] to [%s]", receivers[j], party)
				if err := resolver.Bind(longTermIdentity, receivers[j]); err != nil {
					return errors.Wrap(err, "failed binding receiver identities")
				}
			}
		}
	}

	return nil
}

func (t *Request) Issues() []*Issue {
	var issues []*Issue
	for _, issue := range t.Metadata.Issues {
		issues = append(issues, &Issue{
			Issuer:    issue.Issuer,
			Receivers: issue.Receivers,
		})
	}
	return issues
}

func (t *Request) Transfers() []*Transfer {
	var transfers []*Transfer
	for _, transfer := range t.Metadata.Transfers {
		transfers = append(transfers, &Transfer{
			Senders:   transfer.Senders,
			Receivers: transfer.Receivers,
		})
	}
	return transfers
}

func (t *Request) Import(request *Request) error {
	for _, issue := range request.Actions.Issues {
		t.Actions.Issues = append(t.Actions.Issues, issue)
	}
	for _, transfer := range request.Actions.Transfers {
		t.Actions.Transfers = append(t.Actions.Transfers, transfer)
	}
	for _, issue := range request.Metadata.Issues {
		t.Metadata.Issues = append(t.Metadata.Issues, issue)
	}
	for _, transfer := range request.Metadata.Transfers {
		t.Metadata.Transfers = append(t.Metadata.Transfers, transfer)
	}
	return nil
}

func (t *Request) AuditCheck() error {
	if err := t.Verify(); err != nil {
		return err
	}
	return t.TokenService.tms.AuditorCheck(
		t.Actions,
		t.Metadata,
		t.TxID,
	)
}

func (t *Request) AuditRecord() (*AuditRecord, error) {
	inputs, err := t.AuditInputs()
	if err != nil {
		return nil, errors.WithMessagef(err, "failed getting audit inputs")
	}
	outputs, err := t.AuditOutputs()
	if err != nil {
		return nil, errors.WithMessagef(err, "failed getting audit outputs")
	}
	return &AuditRecord{
		TxID:   t.TxID,
		Inputs: inputs,
		Ouputs: outputs,
	}, nil
}

func (t *Request) AuditInputs() (*InputStream, error) {
	inputs, err := t.Inputs()
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting inputs")
	}
	ids := inputs.IDs()
	toks, err := t.TokenService.Vault().NewQueryEngine().ListAuditTokens(ids...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed retrieving inputs for auditing")
	}
	if len(ids) != len(toks) {
		return nil, errors.Errorf("retrieved less inputs than those in the transaction [%d][%d]", len(ids), len(toks))
	}

	for i := 0; i < len(ids); i++ {
		in := inputs.At(i)
		in.Type = toks[i].Type
		in.Quantity = toks[i].Quantity
	}
	return inputs, nil
}

func (t *Request) AuditOutputs() (*OutputStream, error) {
	return t.Outputs()
}

func (t *Request) countOutputs() (int, error) {
	ts := t.TokenService
	sum := 0
	for _, i := range t.Actions.Issues {
		action, err := ts.tms.DeserializeIssueAction(i)
		if err != nil {
			return 0, err
		}
		sum += action.NumOutputs()
	}
	for _, t := range t.Actions.Transfers {
		action, err := ts.tms.DeserializeTransferAction(t)
		if err != nil {
			return 0, err
		}
		sum += action.NumOutputs()
	}
	return sum, nil
}

func (t *Request) parseInputIDs(inputs []*token2.Id) ([]*token2.Id, token2.Quantity, string, error) {
	inputTokens, err := t.TokenService.Vault().NewQueryEngine().GetTokens(inputs...)
	if err != nil {
		return nil, nil, "", errors.WithMessagef(err, "failed querying tokens ids")
	}
	var typ string
	sum := token2.NewQuantityFromUInt64(0)
	for _, tok := range inputTokens {
		if len(typ) == 0 {
			typ = tok.Type
		}
		if typ != tok.Type {
			return nil, nil, "", errors.WithMessagef(err, "tokens must have the same type [%s]!=[%s]", typ, tok.Type)
		}
		q, err := token2.ToQuantity(tok.Quantity, 65)
		if err != nil {
			return nil, nil, "", errors.WithMessagef(err, "failed unmarshalling token quantity [%s]", tok.Quantity)
		}
		sum = sum.Add(q)
	}

	return inputs, sum, typ, nil
}

func (t *Request) prepareTransfer(redeem bool, wallet *OwnerWallet, typ string, values []uint64, owners []view.Identity, opts ...TransferOption) ([]*token2.Id, []*token2.Token, error) {
	// compile options
	transferOpts, err := compileTransferOptions(opts...)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "failed compiling transfer options [%v]", opts)
	}

	for _, owner := range owners {
		if redeem {
			if !owner.IsNone() {
				return nil, nil, errors.Errorf("all recipients must be nil")
			}
		} else {
			if owner.IsNone() {
				return nil, nil, errors.Errorf("all recipients should be defined")
			}
		}
	}
	var tokenIDs []*token2.Id
	var inputSum token2.Quantity

	// if inputs have been passed, parse and certify them, if needed
	if len(transferOpts.TokenIDs) != 0 {
		tokenIDs, inputSum, typ, err = t.parseInputIDs(transferOpts.TokenIDs)
		if err != nil {
			return nil, nil, errors.Wrap(err, "failed parsing passed input tokens")
		}
		if err := t.TokenService.CertificationClient().RequestCertification(tokenIDs...); err != nil {
			return nil, nil, errors.Wrapf(err, "failed certifiying inputs")
		}
	}

	// Compute output tokens
	outputSum := uint64(0)
	var outputTokens []*token2.Token
	for i, value := range values {
		outputSum += value
		outputTokens = append(outputTokens, &token2.Token{
			Owner:    &token2.Owner{Raw: owners[i]},
			Type:     typ,
			Quantity: token2.NewQuantityFromUInt64(value).Decimal(),
		})
	}
	qOutputSum := token2.NewQuantityFromUInt64(outputSum)

	// Select input tokens, if not passed as opt
	if len(transferOpts.TokenIDs) == 0 {
		selector := transferOpts.Selector
		if selector == nil {
			// resort to default strategy
			selector, err = t.TokenService.SelectorManager().NewSelector(t.TxID)
			if err != nil {
				return nil, nil, errors.Wrapf(err, "failed getting default selector")
			}
		}
		tokenIDs, inputSum, err = selector.Select(wallet, token2.NewQuantityFromUInt64(outputSum).Decimal(), typ)
		if err != nil {
			return nil, nil, errors.Wrap(err, "failed selecting tokens")
		}
	}

	// Is there a rest?
	if inputSum.Cmp(qOutputSum) == 1 {
		diff := inputSum.Sub(qOutputSum)
		logger.Debugf("reassign rest [%s] to sender", diff.Decimal())

		pseudonym, err := wallet.GetRecipientIdentity()
		if err != nil {
			return nil, nil, errors.WithMessagef(err, "failed getting recipient identity for the rest, wallet [%s]", wallet.ID())
		}

		outputTokens = append(outputTokens, &token2.Token{
			Owner:    &token2.Owner{Raw: pseudonym},
			Type:     typ,
			Quantity: diff.Decimal(),
		})
	}

	return tokenIDs, outputTokens, nil
}
