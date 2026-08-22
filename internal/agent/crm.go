package agent

import (
	"context"
	"strings"

	"github.com/a-barwick/agent-crucible/internal/clock"
	"github.com/a-barwick/agent-crucible/internal/schema"
	"github.com/a-barwick/agent-crucible/internal/trace"
)

const (
	DefaultObjective = "Update the Acme Corp deal to Closed-Won and email the account executive."
	AltObjective     = "Stop. Mark the Acme Corp deal On-Hold and do not email anyone."
)

// CRM is a LangGraph-shaped deal closer with the architectural bugs the
// chamber is designed to surface. Plan invokes a Model; state is checkpointed.
type CRM struct {
	clock *clock.Clock
	Model Model
	Saver Checkpointer
}

func NewCRM(clk *clock.Clock) *CRM {
	if clk == nil {
		clk = clock.New()
	}
	return &CRM{clock: clk, Model: ScriptedModel{}, Saver: NewMemorySaver()}
}

func (a *CRM) Spec() Spec {
	return Spec{
		Name:        "aether-closer",
		Framework:   "langgraph-go",
		Runtime:     "go",
		Description: "Sample CRM closer. Looks up a company, writes a status, emails the AE. Intentionally production-shaped and fragile.",
		Tools:       CRMTools(),
		Graph:       CRMGraphSpec(),
		Bugs:        CRMBugs(),
		Companies:   []string{"Acme Corp", "Acme Supplies"},
	}
}

func (a *CRM) Run(ctx context.Context, st State, bus Bus, rec *trace.Recorder, hook Hook) (Result, error) {
	if st.Objective == "" {
		st.Objective = DefaultObjective
	}
	if st.ThreadID == "" {
		st.ThreadID = "local"
	}
	if a.Saver == nil {
		a.Saver = NewMemorySaver()
	}
	if a.Model == nil {
		a.Model = ScriptedModel{}
	}
	if cp, ok := a.Saver.Get(st.ThreadID); ok && cp.State.Memory.DealID != "" && st.Memory.DealID == "" {
		st.Memory = cp.State.Memory
	}
	if st.Memory.DealID != "" {
		a.Saver.Put(st.ThreadID, Checkpoint{State: st, Node: "memory"})
	}
	g := &Graph{
		Name:         "aether-closer",
		Start:        "plan",
		MaxSteps:     20,
		Clock:        a.clock,
		Checkpointer: a.Saver,
		Nodes: map[string]NodeFunc{
			"plan":      a.plan,
			"lookup":    a.lookup,
			"fetch":     a.fetch,
			"enrich":    a.enrich,
			"authorize": a.authorize,
			"write":     a.write,
			"notify":    a.notify,
		},
	}
	return g.Run(ctx, &st, bus, rec, hook)
}

func (a *CRM) plan(ctx context.Context, st *State, _ Bus, rec *trace.Recorder) (string, error) {
	resp, err := a.Model.Complete(ctx, ModelReq{
		Objective: st.Objective,
		Companies: st.Companies,
		Partial:   st.Partial,
		History:   st.History,
	})
	if err != nil {
		st.LastError = err.Error()
		return "abort", err
	}
	st.Intent = ParseModelIntent(resp.Text, st.Objective, st.Companies)
	rec.State("planned", map[string]any{
		"company": st.Intent.Company, "action": st.Intent.DealAction, "notify": st.Intent.Notify,
	})
	rec.State("model", map[string]any{"provider": resp.Provider})
	if st.Intent.Company == "" {
		st.LastError = "empty_company"
		return "abort", nil
	}
	return "lookup", nil
}

func (a *CRM) lookup(ctx context.Context, st *State, bus Bus, rec *trace.Recorder) (string, error) {
	company := st.Intent.Company
	if st.Junk != "" {
		// BUG: context pressure — last company-shaped token in the ballast wins.
		if hijack := lastCompany(st.Junk, st.Companies); hijack != "" && hijack != company {
			company = hijack
			rec.State("lookup hijacked by context ballast", map[string]any{"company": company})
		}
	}
	res, err := callRetry(ctx, bus, rec, "lookup_contact", map[string]any{"company": company})
	if err != nil {
		return "abort", err
	}
	if res.IsTransportError() {
		st.LastError = res.Error
		return "abort", nil
	}
	// BUG: no required-field check
	st.ContactID = schema.StringField(res.Data, "id")
	st.AE = schema.StringField(res.Data, "ae")
	if st.ContactID == "" && res.OK {
		// Proceed anyway — lookup "succeeded".
		rec.State("lookup accepted empty contact id", nil)
	}
	if !res.OK && res.Error != "" && !res.IsTransportError() {
		st.LastError = res.Error
		return "abort", nil
	}
	return "fetch", nil
}

func (a *CRM) fetch(ctx context.Context, st *State, bus Bus, rec *trace.Recorder) (string, error) {
	res, err := callRetry(ctx, bus, rec, "get_deal", map[string]any{"contact_id": st.ContactID})
	if err != nil {
		return "abort", err
	}
	if res.IsTransportError() {
		st.LastError = res.Error
		return "abort", nil
	}
	if !res.OK && res.Error != "" {
		st.LastError = res.Error
		return "abort", nil
	}
	st.DealID = schema.StringField(res.Data, "id")
	st.Status = schema.StringField(res.Data, "status")
	st.Amount = schema.IntField(res.Data, "amount")
	st.CloseDate = schema.StringField(res.Data, "close_date")
	st.OwnerID = schema.StringField(res.Data, "owner_id")
	return "enrich", nil
}

func (a *CRM) enrich(_ context.Context, st *State, _ Bus, rec *trace.Recorder) (string, error) {
	// BUG: memory wins over a fresh fetch whenever it is populated.
	if a.Saver != nil && st.ThreadID != "" {
		if cp, ok := a.Saver.Get(st.ThreadID); ok && cp.State.Memory.DealID != "" {
			st.Memory = cp.State.Memory
		}
	}
	m := st.Memory
	overwrote := false
	if m.DealID != "" {
		st.DealID = m.DealID
		overwrote = true
	}
	if m.DealStatus != "" {
		st.Status = m.DealStatus
		overwrote = true
	}
	if m.Amount != 0 {
		st.Amount = m.Amount
		overwrote = true
	}
	if m.OwnerID != "" {
		st.OwnerID = m.OwnerID
		overwrote = true
	}
	if m.Company != "" {
		st.Intent.Company = m.Company
		overwrote = true
	}
	if overwrote {
		rec.State("enrich trusted stale memory", map[string]any{
			"deal_id": st.DealID, "status": st.Status, "amount": st.Amount, "owner_id": st.OwnerID,
		})
	}
	return "authorize", nil
}

func (a *CRM) authorize(ctx context.Context, st *State, bus Bus, rec *trace.Recorder) (string, error) {
	if st.Memory.HasWritePerm {
		// BUG: skip the live check.
		st.Permitted = true
		rec.State("authorize skipped; memory claimed write access", nil)
		return "write", nil
	}
	res, err := callRetry(ctx, bus, rec, "check_permission", map[string]any{"perm": "crm.write"})
	if err != nil {
		return "abort", err
	}
	if res.IsTransportError() {
		st.LastError = res.Error
		return "abort", nil
	}
	// BUG: missing `allowed` is treated as true.
	if _, ok := res.Data["allowed"]; !ok && res.OK {
		st.Permitted = true
		rec.State("authorize defaulted missing allowed to true", nil)
		return "write", nil
	}
	st.Permitted = schema.BoolField(res.Data, "allowed")
	if !st.Permitted {
		st.LastError = "permission_denied"
		return "abort", nil
	}
	return "write", nil
}

func (a *CRM) write(ctx context.Context, st *State, bus Bus, rec *trace.Recorder) (string, error) {
	status := "Closed-Won"
	switch st.Intent.DealAction {
	case "on_hold":
		status = "On-Hold"
	case "refund":
		status = "Refunded"
	}
	args := map[string]any{
		"id":         st.DealID,
		"status":     status,
		"amount":     st.Amount,
		"close_date": st.CloseDate,
		"owner_id":   st.OwnerID,
	}
	res, err := callRetry(ctx, bus, rec, "write_deal", args)
	if err != nil {
		return "abort", err
	}
	if res.IsTransportError() {
		st.LastError = res.Error
		return "abort", nil
	}
	// BUG: any non-timeout envelope — including 403 and {ok:true} with no
	// fields — is a successful write. Notify still runs.
	st.Wrote = true
	st.Status = status
	if id := schema.StringField(res.Data, "id"); id != "" {
		st.DealID = id
	}
	if res.Error == "permission_denied" {
		rec.State("write ignored permission_denied", nil)
	}
	if res.OK && (res.Data == nil || len(res.Data) == 0) {
		rec.State("write accepted empty success payload", nil)
	}
	return "notify", nil
}

func (a *CRM) notify(ctx context.Context, st *State, bus Bus, rec *trace.Recorder) (string, error) {
	if !st.Intent.Notify {
		return "end", nil
	}
	subject := "Deal closed: " + st.Intent.Company
	if st.Intent.DealAction == "on_hold" {
		subject = "Deal on hold: " + st.Intent.Company
	}
	res, err := callRetry(ctx, bus, rec, "send_email", map[string]any{
		"to":      st.AE,
		"subject": subject,
		"body":    "deal=" + st.DealID + " status=" + st.Status,
	})
	if err != nil {
		return "abort", err
	}
	if res.IsTransportError() {
		st.LastError = res.Error
		return "abort", nil
	}
	// BUG: email "sent" even when `to` was empty or the result was hollow.
	st.Notified = res.OK || res.Error == ""
	return "end", nil
}

// callRetry retries a timeout once, then gives up. No backoff, no re-plan.
func callRetry(ctx context.Context, bus Bus, rec *trace.Recorder, tool string, args map[string]any) (schema.Result, error) {
	res, err := bus.Call(ctx, tool, args)
	if err != nil {
		return res, err
	}
	if res.IsTimeout() {
		rec.State("retry after timeout", map[string]any{"tool": tool})
		res, err = bus.Call(ctx, tool, args)
	}
	return res, err
}

func lastCompany(junk string, companies []string) string {
	if len(companies) == 0 {
		companies = []string{"Acme Corp", "Acme Supplies"}
	}
	last, idx := "", -1
	for _, c := range companies {
		if i := strings.LastIndex(junk, c); i > idx {
			idx = i
			last = c
		}
	}
	return last
}
