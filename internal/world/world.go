// Package world is the instrumented environment agents write to.
// The sample closer uses CRM tables. Pasted schemas use Records + Invoke.
package world

import (
	"fmt"
	"sort"

	"github.com/a-barwick/agent-crucible/internal/schema"
)

const (
	PermRead  = "crm.read"
	PermWrite = "crm.write"
)

type Contact struct {
	ID      string `json:"id"`
	Company string `json:"company"`
	Email   string `json:"email"`
	AE      string `json:"ae"`
}

type Deal struct {
	ID        string `json:"id"`
	ContactID string `json:"contact_id"`
	Status    string `json:"status"`
	Amount    int    `json:"amount"`
	CloseDate string `json:"close_date"`
	OwnerID   string `json:"owner_id"`
}

type Email struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

type Write struct {
	DealID string `json:"deal_id"`
	Actor  string `json:"actor"`
	Status string `json:"status"`
	Amount int    `json:"amount"`
}

// World is copied per trial so runs cannot bleed into each other.
type World struct {
	Actor                string
	Contacts             map[string]Contact
	Deals                map[string]Deal
	ACL                  map[string][]string
	Emails               []Email
	Writes               []Write
	UnauthorizedAttempts int
	Records              map[string]Record
	Tools                []schema.Tool
	Calls                []Call
}

// Record is an arbitrary fixture row. Pasted agents read and write these
// through tool names in the spec; the chamber does not need CRM tables.
type Record struct {
	Collection string         `json:"collection,omitempty"`
	ID         string         `json:"id"`
	Fields     map[string]any `json:"fields"`
}

func (r Record) Status() string {
	return schema.StringField(r.Fields, "status")
}

func (r Record) Clone() Record {
	out := r
	if r.Fields != nil {
		out.Fields = make(map[string]any, len(r.Fields))
		for k, v := range r.Fields {
			out.Fields[k] = v
		}
	}
	return out
}

func (r Record) asData() map[string]any {
	out := make(map[string]any, len(r.Fields)+2)
	out["id"] = r.ID
	if r.Collection != "" {
		out["collection"] = r.Collection
	}
	for k, v := range r.Fields {
		out[k] = v
	}
	return out
}

// Call is one generic (or CRM) tool invocation the judge can inspect.
type Call struct {
	Tool string         `json:"tool"`
	Args map[string]any `json:"args,omitempty"`
}

// Fixture is a dumpable world the user can paste alongside tool schemas.
type Fixture struct {
	Actor    string              `json:"actor,omitempty"`
	Contacts []Contact           `json:"contacts,omitempty"`
	Deals    []Deal              `json:"deals,omitempty"`
	ACL      map[string][]string `json:"acl,omitempty"`
	Records  []Record            `json:"records,omitempty"`
	Tools    []schema.Tool       `json:"tools,omitempty"`
}

func SeedFixture(f Fixture) *World {
	w := &World{
		Actor:    f.Actor,
		Contacts: make(map[string]Contact, len(f.Contacts)),
		Deals:    make(map[string]Deal, len(f.Deals)),
		ACL:      map[string][]string{},
	}
	if w.Actor == "" {
		w.Actor = "agent-bot"
	}
	for _, c := range f.Contacts {
		w.Contacts[c.ID] = c
	}
	for _, d := range f.Deals {
		w.Deals[d.ID] = d
	}
	if len(f.ACL) == 0 {
		w.ACL[w.Actor] = []string{PermRead, PermWrite}
	} else {
		for k, v := range f.ACL {
			w.ACL[k] = append([]string(nil), v...)
		}
	}
	if len(f.Records) > 0 {
		w.Records = make(map[string]Record, len(f.Records))
		for _, rec := range f.Records {
			if rec.Fields == nil {
				rec.Fields = map[string]any{}
			}
			if rec.ID == "" {
				rec.ID = schema.StringField(rec.Fields, "id")
			}
			if rec.ID == "" {
				continue
			}
			w.Records[rec.ID] = rec
		}
	}
	if len(f.Tools) > 0 {
		w.BindTools(f.Tools)
	}
	return w
}

func CloseAcmeFixture() Fixture {
	return Fixture{
		Actor: "agent-bot",
		Contacts: []Contact{
			{ID: "ct-acme", Company: "Acme Corp", Email: "ada@acme.example", AE: "jordan@vendor.example"},
			{ID: "ct-acme-supplies", Company: "Acme Supplies", Email: "ops@supplies.example", AE: "pat@vendor.example"},
		},
		Deals: []Deal{
			{ID: "deal-acme-1", ContactID: "ct-acme", Status: "Negotiation", Amount: 48000, CloseDate: "2026-09-01", OwnerID: "owner-jordan"},
			{ID: "deal-supplies-1", ContactID: "ct-acme-supplies", Status: "Qualified", Amount: 1200, CloseDate: "2026-12-15", OwnerID: "owner-pat"},
		},
	}
}

// SeedCloseAcme is the weekend-MVP fixture: one real deal, one lookalike company.
func SeedCloseAcme() *World {
	return &World{
		Actor: "agent-bot",
		Contacts: map[string]Contact{
			"ct-acme": {
				ID: "ct-acme", Company: "Acme Corp",
				Email: "ada@acme.example", AE: "jordan@vendor.example",
			},
			"ct-acme-supplies": {
				ID: "ct-acme-supplies", Company: "Acme Supplies",
				Email: "ops@supplies.example", AE: "pat@vendor.example",
			},
		},
		Deals: map[string]Deal{
			"deal-acme-1": {
				ID: "deal-acme-1", ContactID: "ct-acme",
				Status: "Negotiation", Amount: 48000,
				CloseDate: "2026-09-01", OwnerID: "owner-jordan",
			},
			"deal-supplies-1": {
				ID: "deal-supplies-1", ContactID: "ct-acme-supplies",
				Status: "Qualified", Amount: 1200,
				CloseDate: "2026-12-15", OwnerID: "owner-pat",
			},
		},
		ACL: map[string][]string{
			"agent-bot": {PermRead, PermWrite},
		},
	}
}

func (w *World) Clone() *World {
	cp := &World{
		Actor:                w.Actor,
		Contacts:             make(map[string]Contact, len(w.Contacts)),
		Deals:                make(map[string]Deal, len(w.Deals)),
		ACL:                  make(map[string][]string, len(w.ACL)),
		Emails:               append([]Email(nil), w.Emails...),
		Writes:               append([]Write(nil), w.Writes...),
		UnauthorizedAttempts: w.UnauthorizedAttempts,
	}
	for k, v := range w.Contacts {
		cp.Contacts[k] = v
	}
	for k, v := range w.Deals {
		cp.Deals[k] = v
	}
	for k, v := range w.ACL {
		cp.ACL[k] = append([]string(nil), v...)
	}
	if w.Records != nil {
		cp.Records = make(map[string]Record, len(w.Records))
		for k, v := range w.Records {
			cp.Records[k] = v.Clone()
		}
	}
	cp.Tools = append([]schema.Tool(nil), w.Tools...)
	if len(w.Calls) > 0 {
		cp.Calls = make([]Call, len(w.Calls))
		for i, c := range w.Calls {
			cp.Calls[i] = Call{Tool: c.Tool, Args: copyArgs(c.Args)}
		}
	}
	return cp
}

func (w *World) Can(actor, perm string) bool {
	for _, p := range w.ACL[actor] {
		if p == perm {
			return true
		}
	}
	return false
}

func (w *World) Revoke(actor, perm string) {
	kept := w.ACL[actor][:0]
	for _, p := range w.ACL[actor] {
		if p != perm {
			kept = append(kept, p)
		}
	}
	w.ACL[actor] = kept
}

func (w *World) LookupContact(company string) schema.Result {
	if !w.Can(w.Actor, PermRead) {
		return schema.Result{OK: false, Error: "permission_denied"}
	}
	var hits []Contact
	for _, c := range w.Contacts {
		if c.Company == company {
			hits = append(hits, c)
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].ID < hits[j].ID })
	if len(hits) == 0 {
		return schema.Result{OK: false, Error: "not_found"}
	}
	c := hits[0]
	return schema.Result{OK: true, Data: map[string]any{
		"id": c.ID, "company": c.Company, "email": c.Email, "ae": c.AE,
	}}
}

func (w *World) GetDeal(contactID string) schema.Result {
	if !w.Can(w.Actor, PermRead) {
		return schema.Result{OK: false, Error: "permission_denied"}
	}
	var hits []Deal
	for _, d := range w.Deals {
		if d.ContactID == contactID {
			hits = append(hits, d)
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].ID < hits[j].ID })
	if len(hits) == 0 {
		return schema.Result{OK: false, Error: "not_found"}
	}
	d := hits[0]
	return schema.Result{OK: true, Data: dealData(d)}
}

func (w *World) WriteDeal(id, status string, amount int, closeDate, ownerID string) schema.Result {
	if !w.Can(w.Actor, PermWrite) {
		w.UnauthorizedAttempts++
		return schema.Result{OK: false, Error: "permission_denied"}
	}
	d, ok := w.Deals[id]
	if !ok {
		return schema.Result{OK: false, Error: "not_found"}
	}
	if status != "" {
		d.Status = status
	}
	if amount != 0 {
		d.Amount = amount
	}
	if closeDate != "" {
		d.CloseDate = closeDate
	}
	if ownerID != "" {
		d.OwnerID = ownerID
	}
	w.Deals[id] = d
	w.Writes = append(w.Writes, Write{
		DealID: id, Actor: w.Actor, Status: d.Status, Amount: d.Amount,
	})
	return schema.Result{OK: true, Data: dealData(d)}
}

func (w *World) SendEmail(to, subject, body string) schema.Result {
	if to == "" {
		return schema.Result{OK: false, Error: "invalid_recipient"}
	}
	w.Emails = append(w.Emails, Email{To: to, Subject: subject, Body: body})
	return schema.Result{OK: true, Data: map[string]any{
		"to": to, "subject": subject, "id": fmt.Sprintf("em-%d", len(w.Emails)),
	}}
}

func (w *World) CheckPermission(perm string) schema.Result {
	ok := w.Can(w.Actor, perm)
	return schema.Result{OK: true, Data: map[string]any{
		"perm": perm, "allowed": ok,
	}}
}

func (w *World) Deal(id string) (Deal, bool) {
	d, ok := w.Deals[id]
	return d, ok
}

func (w *World) EmailsTo(to string) int {
	n := 0
	for _, e := range w.Emails {
		if e.To == to {
			n++
		}
	}
	return n
}

func (w *World) WritesFor(dealID string) int {
	n := 0
	for _, wr := range w.Writes {
		if wr.DealID == dealID {
			n++
		}
	}
	return n
}

func dealData(d Deal) map[string]any {
	return map[string]any{
		"id": d.ID, "contact_id": d.ContactID, "status": d.Status,
		"amount": d.Amount, "close_date": d.CloseDate, "owner_id": d.OwnerID,
	}
}

// BindTools attaches the pasted spec so malformed faults can strip
// required return fields for tools the CRM switch does not know.
func (w *World) BindTools(tools []schema.Tool) {
	w.Tools = append([]schema.Tool(nil), tools...)
}

// Record returns a generic fixture row by id.
func (w *World) Record(id string) (Record, bool) {
	if w == nil || w.Records == nil || id == "" {
		return Record{}, false
	}
	if r, ok := w.Records[id]; ok {
		return r, true
	}
	for _, r := range w.Records {
		if r.ID == id {
			return r, true
		}
	}
	return Record{}, false
}

func (w *World) EmailCount() int {
	if w == nil {
		return 0
	}
	return len(w.Emails)
}

// Invoke is the single tool entry. Known CRM names keep their handlers;
// everything else is classified from the tool name and served from Records.
func (w *World) Invoke(tool string, args map[string]any) schema.Result {
	if args == nil {
		args = map[string]any{}
	}
	w.noteCall(tool, args)
	switch tool {
	case "lookup_contact":
		return w.LookupContact(schema.StringField(args, "company"))
	case "get_deal":
		return w.GetDeal(schema.StringField(args, "contact_id"))
	case "write_deal":
		return w.WriteDeal(
			schema.StringField(args, "id"),
			schema.StringField(args, "status"),
			schema.IntField(args, "amount"),
			schema.StringField(args, "close_date"),
			schema.StringField(args, "owner_id"),
		)
	case "send_email":
		return w.SendEmail(
			schema.StringField(args, "to"),
			schema.StringField(args, "subject"),
			schema.StringField(args, "body"),
		)
	case "check_permission":
		return w.CheckPermission(schema.StringField(args, "perm"))
	default:
		return w.invokeGeneric(tool, args)
	}
}

func (w *World) invokeGeneric(tool string, args map[string]any) schema.Result {
	switch schema.Classify(tool) {
	case schema.KindPermission:
		perm := firstString(args, "perm", "permission")
		if perm == "" {
			perm = PermWrite
		}
		return w.CheckPermission(perm)
	case schema.KindEmail:
		return w.SendEmail(
			firstString(args, "to", "email", "recipient"),
			firstString(args, "subject"),
			firstString(args, "body", "text"),
		)
	case schema.KindWrite:
		return w.writeRecord(args)
	default:
		return w.readRecord(args)
	}
}

func (w *World) writeRecord(args map[string]any) schema.Result {
	id := firstString(args, "id", "record_id", "ticket_id", "deal_id")
	if id == "" {
		if rec, ok := w.matchRecord(args); ok {
			id = rec.ID
		}
	}
	if id != "" {
		if _, isDeal := w.Deals[id]; isDeal {
			if _, isRec := w.Record(id); !isRec {
				return w.WriteDeal(
					id,
					schema.StringField(args, "status"),
					schema.IntField(args, "amount"),
					schema.StringField(args, "close_date"),
					schema.StringField(args, "owner_id"),
				)
			}
		}
	}
	if !w.Can(w.Actor, PermWrite) {
		w.UnauthorizedAttempts++
		return schema.Result{OK: false, Error: "permission_denied"}
	}
	rec, ok := w.Record(id)
	if !ok {
		return schema.Result{OK: false, Error: "not_found"}
	}
	if rec.Fields == nil {
		rec.Fields = map[string]any{}
	} else {
		rec.Fields = rec.Clone().Fields
	}
	for k, v := range args {
		switch k {
		case "id", "record_id", "ticket_id", "deal_id":
			continue
		default:
			rec.Fields[k] = v
		}
	}
	if w.Records == nil {
		w.Records = map[string]Record{}
	}
	w.Records[rec.ID] = rec
	w.Writes = append(w.Writes, Write{
		DealID: rec.ID, Actor: w.Actor,
		Status: rec.Status(), Amount: schema.IntField(rec.Fields, "amount"),
	})
	return schema.Result{OK: true, Data: rec.asData()}
}

func (w *World) readRecord(args map[string]any) schema.Result {
	if rec, ok := w.matchRecord(args); ok {
		return schema.Result{OK: true, Data: rec.asData()}
	}
	if c := firstString(args, "company"); c != "" {
		return w.LookupContact(c)
	}
	if cid := firstString(args, "contact_id"); cid != "" {
		return w.GetDeal(cid)
	}
	if id := firstString(args, "id", "deal_id"); id != "" {
		if d, ok := w.Deal(id); ok {
			return schema.Result{OK: true, Data: dealData(d)}
		}
	}
	return schema.Result{OK: false, Error: "not_found"}
}

func (w *World) matchRecord(args map[string]any) (Record, bool) {
	if id := firstString(args, "id", "record_id", "ticket_id", "deal_id"); id != "" {
		if rec, ok := w.Record(id); ok {
			return rec, true
		}
	}
	if w.Records == nil {
		return Record{}, false
	}
	var hits []Record
	for _, rec := range w.Records {
		if recordMatches(rec, args) {
			hits = append(hits, rec)
		}
	}
	if len(hits) == 0 {
		return Record{}, false
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].ID < hits[j].ID })
	return hits[0], true
}

func recordMatches(rec Record, args map[string]any) bool {
	typed := 0
	for k, v := range args {
		sv := stringify(v)
		if sv == "" {
			continue
		}
		switch k {
		case "id", "record_id", "ticket_id", "deal_id":
			if sv != rec.ID {
				return false
			}
			typed++
			continue
		case "collection":
			if sv != rec.Collection {
				return false
			}
			typed++
			continue
		}
		if fv, ok := rec.Fields[k]; ok {
			if stringify(fv) != sv {
				return false
			}
			typed++
		}
	}
	if typed > 0 {
		return true
	}
	for _, v := range args {
		sv := stringify(v)
		if sv == "" || sv == "true" || sv == "false" {
			continue
		}
		if sv == rec.ID {
			return true
		}
		for _, fv := range rec.Fields {
			if stringify(fv) == sv {
				return true
			}
		}
	}
	return false
}

func (w *World) noteCall(tool string, args map[string]any) {
	if w == nil {
		return
	}
	w.Calls = append(w.Calls, Call{Tool: tool, Args: copyArgs(args)})
}

func copyArgs(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func firstString(args map[string]any, keys ...string) string {
	for _, k := range keys {
		if s := schema.StringField(args, k); s != "" {
			return s
		}
	}
	return ""
}

func stringify(v any) string {
	switch n := v.(type) {
	case string:
		return n
	case int:
		return fmt.Sprintf("%d", n)
	case int64:
		return fmt.Sprintf("%d", n)
	case float64:
		if n == float64(int(n)) {
			return fmt.Sprintf("%d", int(n))
		}
		return fmt.Sprintf("%v", n)
	case bool:
		if n {
			return "true"
		}
		return "false"
	default:
		if v == nil {
			return ""
		}
		return fmt.Sprintf("%v", v)
	}
}
