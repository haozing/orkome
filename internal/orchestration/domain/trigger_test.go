package domain

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var agentA = AgentFacts{ID: "aaaa"}
var agentB = AgentFacts{ID: "bbbb", Archived: true}
var agentL = AgentFacts{ID: "cccc", LeaderOfSquads: []string{"e111"}}
var agentW = AgentFacts{ID: "dddd", MemberOfSquads: []string{"e111"}}

var allAgents = []AgentFacts{agentA, agentB, agentL, agentW}
var allSquads = []SquadFacts{{ID: "e111", LeaderID: "cccc", MemberIDs: []string{"dddd"}}}

func allowAll(AgentFacts) bool { return true }
func denyB(a AgentFacts) bool  { return a.ID != "bbbb" }

func TestTree_1_NoteComment(t *testing.T) {
	d := DecisionTree(CommentFacts{Content: "/note internal note", AssigneeType: "agent", AssigneeID: "aaaa"}, allAgents, allSquads, allowAll)
	assert.Empty(t, d.Triggers)
}

func TestTree_3_ExplicitMentionExclusive(t *testing.T) {
	d := DecisionTree(CommentFacts{
		Content: "please handle [@A](mention://agent/aaaa)", AssigneeType: "agent", AssigneeID: "cccc",
	}, allAgents, allSquads, allowAll)
	assert.Len(t, d.Triggers, 1)
	assert.Equal(t, "aaaa", d.Triggers[0].AgentID)
	assert.Equal(t, "3.explicit_mention", d.Triggers[0].Reason)
}

func TestTree_3_SquadMentionRoutesLeader(t *testing.T) {
	d := DecisionTree(CommentFacts{
		Content: "[[@team](mention://squad/e111)](x)",
	}, allAgents, allSquads, allowAll)

	d = DecisionTree(CommentFacts{
		Content: "over to you [@team](mention://squad/e111)",
	}, allAgents, allSquads, allowAll)
	assert.Len(t, d.Triggers, 1)
	assert.Equal(t, "cccc", d.Triggers[0].AgentID)
	assert.Equal(t, "3.explicit_squad_mention", d.Triggers[0].Reason)
}

func TestTree_3_SuppressedArchived_I6Order(t *testing.T) {
	d := DecisionTree(CommentFacts{Content: "[@B](mention://agent/bbbb)"}, allAgents, allSquads, allowAll)
	assert.Empty(t, d.Triggers)
	assert.Len(t, d.Suppressions, 1)
	assert.Equal(t, "target_unavailable", d.Suppressions[0].Reason)
	d2 := DecisionTree(CommentFacts{Content: "[@B](mention://agent/bbbb)"}, allAgents, allSquads, denyB)
	assert.Equal(t, "invocation_not_allowed", d2.Suppressions[0].Reason)
}

func TestTree_4_AllSuppressesFallback(t *testing.T) {
	d := DecisionTree(CommentFacts{Content: "[@all](mention://all/) everyone please look", AssigneeType: "agent", AssigneeID: "aaaa"}, allAgents, allSquads, allowAll)
	assert.Empty(t, d.Triggers, "@all overrides the implicit fallback")
}

func TestTree_5_MemberMentionKillsAllTriggers(t *testing.T) {

	d := DecisionTree(CommentFacts{
		Content:    "[@zhang3](mention://user/aa11) look at this",
		AuthorType: "user", AuthorID: "u222",
		AssigneeType: "agent", AssigneeID: "aaaa",
	}, allAgents, allSquads, allowAll)
	assert.Empty(t, d.Triggers, "@member comment triggers nothing overall")
	assert.Empty(t, d.Suppressions)

	d2 := DecisionTree(CommentFacts{
		Content:    "[@zhang3](mention://user/aa11) please double-check",
		AuthorType: "user", AuthorID: "u222",
		ParentAuthorType: "agent", ParentAuthorID: "aaaa",
	}, allAgents, allSquads, allowAll)
	assert.Empty(t, d2.Triggers, "7a reply path does not trigger either")

	d3 := DecisionTree(CommentFacts{
		Content: "[@A](mention://agent/aaaa) and [@zhang3](mention://user/aa11) review together",
	}, allAgents, allSquads, allowAll)
	assert.Len(t, d3.Triggers, 1, "explicit @agent is exclusive, unaffected by @member")
}

func TestTree_6a_AgentAuthor_LeaderLoopback(t *testing.T) {
	d := DecisionTree(CommentFacts{
		AuthorType: "agent", AuthorID: "dddd",
	}, allAgents, allSquads, allowAll)
	assert.Len(t, d.Triggers, 1)
	assert.Equal(t, "cccc", d.Triggers[0].AgentID)
	assert.Equal(t, "6a.agent_author_squad_leader", d.Triggers[0].Reason)
}

func TestTree_6b_WorkItemSquad_LeaderFallback(t *testing.T) {
	d := DecisionTree(CommentFacts{
		AuthorType: "agent", AuthorID: "aaaa", AssigneeType: "squad", AssigneeID: "e111",
	}, allAgents, allSquads, allowAll)
	assert.Equal(t, "cccc", d.Triggers[0].AgentID)
	assert.Equal(t, "6b.work_item_squad_leader", d.Triggers[0].Reason)
}

func TestTree_6c_AgentAuthorNoPath(t *testing.T) {
	d := DecisionTree(CommentFacts{AuthorType: "agent", AuthorID: "aaaa"}, allAgents, allSquads, allowAll)
	assert.Empty(t, d.Triggers)
	d2 := DecisionTree(CommentFacts{AuthorType: "system"}, allAgents, allSquads, allowAll)
	assert.Empty(t, d2.Triggers)
}

func TestTree_7a_ReplyToAgent_Exclusive(t *testing.T) {
	d := DecisionTree(CommentFacts{
		AuthorType: "user", AuthorID: "u1",
		ParentAuthorType: "agent", ParentAuthorID: "dddd",
		AssigneeType: "agent", AssigneeID: "aaaa",
	}, allAgents, allSquads, allowAll)
	assert.Len(t, d.Triggers, 1)
	assert.Equal(t, "dddd", d.Triggers[0].AgentID)
	assert.Equal(t, "7a.reply_to_agent", d.Triggers[0].Reason)
}

func TestTree_7b_RootOwnerRouting(t *testing.T) {

	d := DecisionTree(CommentFacts{
		AuthorType: "user", AuthorID: "u1",
		ParentAuthorType: "user", ParentAuthorID: "u2",
		RootAuthorType: "agent", RootAuthorID: "dddd",
	}, allAgents, allSquads, allowAll)
	assert.Empty(t, d.Triggers)

	d2 := DecisionTree(CommentFacts{
		AuthorType: "user", AuthorID: "u1",
		ParentAuthorType: "user", ParentAuthorID: "u2",
		RootAuthorType: "user", RootAuthorID: "u9",
		HistoryAgentIDs: []string{"aaaa", "dddd"},
	}, allAgents, allSquads, allowAll)
	assert.Len(t, d2.Triggers, 2)
	reasons := []string{d2.Triggers[0].Reason, d2.Triggers[1].Reason}
	assert.Contains(t, reasons, "7b.thread_root_owner")
}

func TestTree_7c_WorkItemAssigneeFallback(t *testing.T) {
	d := DecisionTree(CommentFacts{
		AuthorType: "user", AuthorID: "u1", AssigneeType: "agent", AssigneeID: "aaaa",
	}, allAgents, allSquads, allowAll)
	assert.Equal(t, "7c.work_item_assignee", d.Triggers[0].Reason)

	d2 := DecisionTree(CommentFacts{
		AuthorType: "user", AuthorID: "u1", AssigneeType: "squad", AssigneeID: "e111",
	}, allAgents, allSquads, allowAll)
	assert.Equal(t, "cccc", d2.Triggers[0].AgentID)
	assert.Equal(t, "7c.work_item_squad_leader", d2.Triggers[0].Reason)

	d3 := DecisionTree(CommentFacts{AuthorType: "user", AuthorID: "u1", AssigneeType: "none"}, allAgents, allSquads, allowAll)
	assert.Empty(t, d3.Triggers)
}

func TestClassifyMiss(t *testing.T) {
	assert.Equal(t, MissNone, ClassifyMiss(true, false))
	assert.Equal(t, MissActive, ClassifyMiss(false, true))
	assert.Equal(t, MissNew, ClassifyMiss(false, false))
}

var _ = strings.TrimSpace


// ---- 小队编排标志传播（真模型 E2E 断裂修复的回归锁）----

func squadFactsFixture() (CommentFacts, []AgentFacts, []SquadFacts) {
	f := CommentFacts{WorkspaceID: "ws", WorkItemID: "wi", AuthorType: "user", AuthorID: "human"}
	agents := []AgentFacts{
		{ID: "leader", Name: "队长"},
		{ID: "member-a", Name: "成员A", MemberOfSquads: []string{"e222"}},
	}
	squads := []SquadFacts{{ID: "e222", LeaderID: "leader", MemberIDs: []string{"leader", "member-a"}}}
	return f, agents, squads
}

func TestTree_3_ExplicitSquadMention_LeaderCarriesFlags(t *testing.T) {
	f, agents, squads := squadFactsFixture()
	f.Content = "[@e2e小队](mention://squad/e222) 开始"
	d := DecisionTree(f, agents, squads, func(AgentFacts) bool { return true })
	require.Len(t, d.Triggers, 1)
	tr := d.Triggers[0]
	assert.Equal(t, "leader", tr.AgentID)
	assert.True(t, tr.IsLeaderTask, "leader 任务必须带编排标志")
	assert.Equal(t, "e222", tr.SquadID)
}

func TestTree_6a_MemberComment_LeaderCarriesFlags(t *testing.T) {
	f, agents, squads := squadFactsFixture()
	f.AuthorType = "agent"
	f.AuthorID = "member-a"
	f.Content = "成员汇报：部分完成"
	d := DecisionTree(f, agents, squads, func(AgentFacts) bool { return true })
	require.Len(t, d.Triggers, 1)
	tr := d.Triggers[0]
	assert.Equal(t, "leader", tr.AgentID)
	assert.True(t, tr.IsLeaderTask)
	assert.Equal(t, "e222", tr.SquadID)
}
