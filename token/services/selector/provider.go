/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/
package selector

import (
	"sync"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/platform/view"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/flogging"
	"github.com/hyperledger-labs/fabric-token-sdk/token"
	token2 "github.com/hyperledger-labs/fabric-token-sdk/token/token"
)

var logger = flogging.MustGetLogger("token-sdk.selector")

type Transaction interface {
	ID() string
	Network() string
	Channel() string
	Namespace() string
}

type Repository interface {
	Next(typ string, of token.OwnerFilter) (*token2.UnspentToken, error)
}

type LockerProvider interface {
	New(network, channel, namespace string) Locker
}

type selectorService struct {
	sp                   view.ServiceProvider
	numRetry             int
	timeout              time.Duration
	requestCertification bool

	lock           sync.Mutex
	lockerProvider LockerProvider
	lockers        map[string]Locker
}

func NewProvider(sp view.ServiceProvider, lockerProvider LockerProvider, numRetry int, timeout time.Duration) *selectorService {
	return &selectorService{
		sp:                   sp,
		lockerProvider:       lockerProvider,
		lockers:              map[string]Locker{},
		numRetry:             numRetry,
		timeout:              timeout,
		requestCertification: true,
	}
}

func (s *selectorService) SelectorManager(network string, channel string, namespace string) token.SelectorManager {
	tms := token.GetManagementService(
		s.sp,
		token.WithNetwork(network),
		token.WithChannel(channel),
		token.WithNamespace(namespace),
	)

	s.lock.Lock()
	defer s.lock.Unlock()

	key := tms.Network() + tms.Channel() + tms.Namespace()
	locker, ok := s.lockers[key]
	if !ok {
		logger.Debugf("new in-memory locker for [%s:%s:%s]", tms.Network(), tms.Channel(), tms.Namespace())
		locker = s.lockerProvider.New(network, channel, namespace)
		s.lockers[key] = locker
	} else {
		logger.Debugf("in-memory selector for [%s:%s:%s] exists", tms.Network(), tms.Channel(), tms.Namespace())
	}

	return newManager(
		locker,
		func() QueryService {
			return tms.Vault().NewQueryEngine()
		},
		tms.CertificationClient(),
		s.numRetry,
		s.timeout,
		s.requestCertification,
	)
}

func (s *selectorService) SetNumRetries(n uint) {
	s.numRetry = int(n)
}

func (s *selectorService) SetRetryTimeout(t time.Duration) {
	s.timeout = t
}

func (s *selectorService) SetRequestCertification(v bool) {
	s.requestCertification = v
}
