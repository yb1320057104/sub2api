package service

import "context"

type radarCandyRequestState struct {
	accountID   int64
	status      string
	schedulable bool
}

func radarCandyState(account *Account) radarCandyRequestState {
	return radarCandyRequestState{accountID: account.ID, status: account.Status, schedulable: account.Schedulable}
}

func radarCandyClaimStates(run *RadarRun) (radarCandyRequestState, radarCandyRequestState, error) {
	if run == nil || run.probeAccount == nil || run.credentialAccount == nil {
		return radarCandyRequestState{}, radarCandyRequestState{}, candyProbeFailure("本轮账号状态快照缺失，停止此模型的后续请求；此失败不作为转组依据。")
	}
	return radarCandyState(run.probeAccount), radarCandyState(run.credentialAccount), nil
}

// This check belongs to new request admission, not the run heartbeat: a 401
// from a sibling must stop reusing its credentials without discarding answers
// already completed or still arriving. Preserve the initial manual-test state.
func (s *AccountCandyTestService) checkRadarRequestState(ctx context.Context, account, credential radarCandyRequestState) error {
	states := []radarCandyRequestState{account}
	if credential.accountID != account.accountID {
		states = append(states, credential)
	}
	for _, expected := range states {
		current, err := s.accountRepo.GetByID(ctx, expected.accountID)
		if err != nil {
			return err
		}
		if current == nil || current.Status != expected.status || current.Schedulable != expected.schedulable {
			return candyProbeFailure("账号或凭据母账号状态已变化，停止此模型的后续请求；此失败不作为转组依据。")
		}
	}
	return nil
}
