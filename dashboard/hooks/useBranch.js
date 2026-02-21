import { createContext, useContext } from 'react';

// Branch context for sharing current branch across all pages
export const BranchContext = createContext({
  branch: null,
  branches: [],
  branchIssue: null,
  branchMeta: {},      // { domain, category, topic, issueNumber, title }
  loading: false,
  switchBranch: async () => {},
  refreshBranch: async () => {},
  openBranchPicker: () => {},
});

export function useBranch() {
  return useContext(BranchContext);
}
