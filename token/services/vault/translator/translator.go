/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/
package translator

import (
	"strconv"

	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/flogging"
	"github.com/pkg/errors"

	"github.com/hyperledger-labs/fabric-token-sdk/token/services/vault/keys"
	token2 "github.com/hyperledger-labs/fabric-token-sdk/token/token"
)

var logger = flogging.MustGetLogger("token-sdk.vault.translator")

// Translator validates token requests and generates the corresponding RWSets
type Translator struct {
	IssuingValidator IssuingValidator
	RWSet            RWSet
	TxID             string
	counter          int
	namespace        string
}

func New(issuingValidator IssuingValidator, txID string, rwSet RWSet, namespace string) *Translator {
	w := &Translator{
		IssuingValidator: issuingValidator,
		RWSet:            rwSet,
		TxID:             txID,
		counter:          0,
		namespace:        namespace,
	}

	return w
}

// Write checks that transactions are correct wrt. the most recent rwset state.
// Write checks are ones that shall be done sequentially, since transactions within a block may introduce dependencies.
func (w *Translator) Write(action interface{}) error {
	logger.Debugf("checking transaction with txID '%s'", w.TxID)

	err := w.checkProcess(action)
	if err != nil {
		return err
	}

	logger.Debugf("committing transaction with txID '%s'", w.TxID)
	err = w.commitProcess(action)
	if err != nil {
		logger.Errorf("error committing transaction with txID '%s': %s", w.TxID, err)
		return err
	}
	logger.Debugf("successfully processed transaction with txID '%s'", w.TxID)
	return nil
}

func (w *Translator) CommitTokenRequest(raw []byte) error {
	key, err := keys.CreateTokenRequestKey(w.TxID)
	if err != nil {
		return errors.Errorf("can't create for token request '%s'", w.TxID)
	}
	tr, err := w.RWSet.GetState(w.namespace, key)
	if err != nil {
		return errors.Wrapf(err, "failed to write token request'%s'", w.TxID)
	}
	if tr != nil {
		return errors.Wrapf(errors.New("token request with same ID already exists"), "failed to write token request'%s'", w.TxID)
	}
	err = w.RWSet.SetState(w.namespace, key, raw)
	if err != nil {
		return errors.Wrapf(err, "failed to write token request'%s'", w.TxID)
	}
	return nil
}

func (w *Translator) checkProcess(action interface{}) error {
	if err := w.checkAction(action); err != nil {
		return err
	}
	return nil
}

func (w *Translator) checkAction(tokenAction interface{}) error {
	switch action := tokenAction.(type) {
	case IssueAction:
		return w.checkIssue(action)
	case TransferAction:
		return w.checkTransfer(action)
	case SetupAction:
		return nil
	default:
		return errors.Errorf("unknown token action: %T", action)
	}
}

func (w *Translator) checkIssue(issue IssueAction) error {
	// check if issuer is allowed to issue type
	err := w.checkIssuePolicy(issue)
	if err != nil {
		return errors.Wrapf(err, "invalid issue: verification of issue policy failed")
	}

	// check if the keys of issued tokens aren't already used.
	// check is assigned owners are valid
	for i := 0; i < issue.NumOutputs(); i++ {
		err = w.checkTokenDoesNotExist(w.counter+i, w.TxID)
		if err != nil {
			return err
		}
	}
	return nil
}

func (w *Translator) checkTransfer(t TransferAction) error {
	keys, err := t.GetInputs()
	if err != nil {
		return errors.Wrapf(err, "invalid transfer: failed getting input IDs")
	}
	if !t.IsGraphHiding() {
		for _, key := range keys {
			bytes, err := w.RWSet.GetState(w.namespace, key)
			if err != nil {
				return errors.Wrapf(err, "invalid transfer: failed getting state [%s]", key)
			}
			if len(bytes) == 0 {
				return errors.Errorf("invalid transfer: input is already spent [%s]", key)
			}
		}
	} else {
		for _, key := range keys {
			bytes, err := w.RWSet.GetState(w.namespace, key)
			if err != nil {
				return errors.Wrapf(err, "invalid transfer: failed getting state [%s]", key)
			}
			if len(bytes) != 0 {
				return errors.Errorf("invalid transfer: input is already spent [%s:%v]", key, bytes)
			}
		}
	}
	// check if the keys of the new tokens aren't already used.
	for i := 0; i < t.NumOutputs(); i++ {
		if !t.IsRedeemAt(i) {
			// this is not a redeemed output
			err := w.checkTokenDoesNotExist(w.counter+i, w.TxID)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (w *Translator) checkTokenDoesNotExist(index int, txID string) error {
	tokenKey, err := keys.CreateTokenKey(txID, index)
	if err != nil {
		return errors.Wrapf(err, "error creating output ID")
	}

	outputBytes, err := w.RWSet.GetState(w.namespace, tokenKey)
	if err != nil {
		return err
	}
	if len(outputBytes) != 0 {
		return errors.Errorf("token already exists: %s", tokenKey)
	}
	return nil
}

func (w *Translator) checkIssuePolicy(issue IssueAction) error {
	// TODO: retrieve type from action
	return w.IssuingValidator.Validate(issue.GetIssuer(), "")
}

func (w *Translator) commitProcess(action interface{}) error {
	logger.Debugf("committing action with txID '%s'", w.TxID)
	err := w.commitAction(action)
	if err != nil {
		logger.Errorf("error committing action with txID '%s': %s", w.TxID, err)
		return err
	}

	logger.Debugf("action with txID '%s' committed successfully", w.TxID)
	return nil
}

func (w *Translator) commitAction(tokenAction interface{}) (err error) {
	switch action := tokenAction.(type) {
	case IssueAction:
		err = w.commitIssueAction(action)
	case TransferAction:
		err = w.commitTransferAction(action)
	case SetupAction:
		err = w.commitSetupAction(action)
	}
	return
}

func (w *Translator) commitSetupAction(setup SetupAction) error {
	raw, err := setup.GetSetupParameters()
	if err != nil {
		return err
	}
	setupKey, err := keys.CreateSetupKey()
	if err != nil {
		return err
	}
	err = w.RWSet.SetState(w.namespace, setupKey, raw)
	if err != nil {
		return err
	}
	return nil
}

func (w *Translator) commitIssueAction(issueAction IssueAction) error {
	base := w.counter

	outputs, err := issueAction.GetSerializedOutputs()
	if err != nil {
		return err
	}
	for i, output := range outputs {
		outputID, err := keys.CreateTokenKey(w.TxID, base+i)
		if err != nil {
			return errors.Errorf("error creating output ID: %s", err)
		}

		if err := w.RWSet.SetState(w.namespace, outputID, output); err != nil {
			return err
		}

		if err := w.RWSet.SetStateMetadata(w.namespace, outputID,
			map[string][]byte{
				keys.Action: []byte(keys.ActionIssue),
			},
		); err != nil {
			return err
		}
	}
	w.counter = w.counter + len(outputs)
	return nil
}

// commitTransferAction is called for both transfer and redeem transactions
// Check the owner of each output to determine how to generate the key
func (w *Translator) commitTransferAction(transferAction TransferAction) error {
	base := w.counter
	for i := 0; i < transferAction.NumOutputs(); i++ {
		if !transferAction.IsRedeemAt(i) {
			outputID, err := keys.CreateTokenKey(w.TxID, base+i)
			if err != nil {
				return errors.Errorf("error creating output ID: %s", err)
			}

			bytes, err := transferAction.SerializeOutputAt(i)
			if err != nil {
				return err
			}
			err = w.RWSet.SetState(w.namespace, outputID, bytes)
			if err != nil {
				return err
			}
			err = w.RWSet.SetStateMetadata(w.namespace, outputID, map[string][]byte{keys.Action: []byte(keys.ActionTransfer)})
			if err != nil {
				return err
			}
		}
	}
	ids, err := transferAction.GetInputs()
	if err != nil {
		return err
	}
	err = w.spendTokens(ids, transferAction.IsGraphHiding())
	if err != nil {
		return err
	}
	w.counter = w.counter + transferAction.NumOutputs()
	return nil
}

func (w *Translator) spendTokens(ids []string, graphHiding bool) error {
	if !graphHiding {
		for _, id := range ids {
			logger.Debugf("Delete state %s\n", id)
			err := w.RWSet.DeleteState(w.namespace, id)
			if err != nil {
				return err
			}

			logger.Debugf("Delete state metadata %s\n", id)
			err = w.RWSet.SetStateMetadata(w.namespace, id, nil)
			if err != nil {
				return err
			}
		}
	} else {
		for _, id := range ids {
			logger.Debugf("add serial number %s\n", id)
			err := w.RWSet.SetState(w.namespace, id, []byte(strconv.FormatBool(true)))
			if err != nil {
				return errors.Wrapf(err, "failed to add serial number %s", id)
			}
		}
	}

	return nil
}

func (w *Translator) ReadSetupParameters() ([]byte, error) {
	setupKey, err := keys.CreateSetupKey()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create setup key")
	}
	raw, err := w.RWSet.GetState(w.namespace, setupKey)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get setup parameters")
	}
	return raw, nil
}

func (w *Translator) QueryTokens(ids []*token2.Id) ([][]byte, error) {
	var res [][]byte
	var errs []error
	for _, id := range ids {
		outputID, err := keys.CreateTokenKey(id.TxId, int(id.Index))
		if err != nil {
			errs = append(errs, errors.Errorf("error creating output ID: %s", err))
			continue
			// return nil, errors.Errorf("error creating output ID: %s", err)
		}
		logger.Debugf("query state [%s:%s]", id, outputID)
		bytes, err := w.RWSet.GetState(w.namespace, outputID)
		if err != nil {
			errs = append(errs, errors.Wrapf(err, "failed getting output for [%s]", outputID))
			// return nil, errors.Wrapf(err, "failed getting output for [%s]", outputID)
			continue
		}
		if len(bytes) == 0 {
			errs = append(errs, errors.Errorf("output for key [%s] does not exist", outputID))
			// return nil, errors.Errorf("output for key [%s] does not exist", outputID)
			continue
		}
		res = append(res, bytes)
	}
	if len(errs) != 0 {
		return nil, errors.Errorf("failed quering tokens [%v] with errs [%d][%v]", ids, len(errs), errs)
	}
	return res, nil
}
