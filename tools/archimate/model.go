package main

import (
	"bytes"
	"fmt"
	"os"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Model is docs/architecture/model.yaml: the elements and relationships of the
// architecture, and one view per Mermaid diagram in the architecture docs.
type Model struct {
	Name          string         `yaml:"name"`
	Documentation string         `yaml:"documentation"`
	Elements      []Element      `yaml:"elements"`
	Relationships []Relationship `yaml:"relationships"`
	Views         []View         `yaml:"views"`
}

type Element struct {
	ID   string `yaml:"id"`
	Type string `yaml:"type"` // an ArchiMate 3.1 element type, e.g. ApplicationComponent
	Name string `yaml:"name"`
	Doc  string `yaml:"doc"`
}

type Relationship struct {
	ID     string `yaml:"id"` // optional; defaults to source-type-target
	Type   string `yaml:"type"`
	Source string `yaml:"source"`
	Target string `yaml:"target"`
	Name   string `yaml:"name"`
	Access string `yaml:"access"` // Access only: Read, Write, ReadWrite or Access
}

// View is one diagram. Its nodes are keyed by the Mermaid node ids of the
// diagram tagged `%% view: <ID>` in Doc, so the two can be compared.
type View struct {
	ID    string   `yaml:"id"`
	Doc   string   `yaml:"doc"`
	Name  string   `yaml:"name"`
	Rels  []string `yaml:"rels"` // relationships to draw; empty means every one between its elements
	Nodes []Node   `yaml:"nodes"`
}

type Node struct {
	M  string `yaml:"m"`  // Mermaid node id
	El string `yaml:"el"` // element id
	In string `yaml:"in"` // Mermaid id of the enclosing node, if nested
}

func load(path string) (*Model, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Model
	dec := yaml.NewDecoder(bytes.NewReader(src))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for i := range m.Relationships {
		r := &m.Relationships[i]
		if r.ID == "" {
			r.ID = r.Source + "-" + strings.ToLower(r.Type) + "-" + r.Target
		}
	}
	return &m, nil
}

// elementTypes are the element types of the ArchiMate 3.1 exchange format.
var elementTypes = strings.Fields(`BusinessActor BusinessRole BusinessCollaboration BusinessInterface
	BusinessProcess BusinessFunction BusinessInteraction BusinessEvent BusinessService BusinessObject
	Contract Representation Product ApplicationComponent ApplicationCollaboration ApplicationInterface
	ApplicationFunction ApplicationInteraction ApplicationProcess ApplicationEvent ApplicationService
	DataObject Node Device SystemSoftware TechnologyCollaboration TechnologyInterface Path
	CommunicationNetwork TechnologyFunction TechnologyProcess TechnologyInteraction TechnologyEvent
	TechnologyService Artifact Equipment Facility DistributionNetwork Material Stakeholder Driver
	Assessment Goal Outcome Principle Requirement Constraint Meaning Value Resource Capability
	CourseOfAction ValueStream WorkPackage Deliverable ImplementationEvent Plateau Gap Grouping Location`)

// allowed lists the source, relationship and target types this model may use beyond
// Association (always allowed) and Composition, Aggregation and Specialization between
// elements of the same type. Each is valid in the ArchiMate 3.1 relationship tables;
// keeping the list short keeps the model importable without a full rule engine.
var allowed = map[[3]string]bool{
	{"ApplicationComponent", "Access", "DataObject"}:             true,
	{"ApplicationProcess", "Access", "DataObject"}:               true,
	{"TechnologyProcess", "Access", "Artifact"}:                  true,
	{"ApplicationComponent", "Serving", "ApplicationComponent"}:  true,
	{"ApplicationComponent", "Serving", "BusinessActor"}:         true,
	{"SystemSoftware", "Serving", "ApplicationComponent"}:        true,
	{"ApplicationComponent", "Flow", "ApplicationComponent"}:     true,
	{"ApplicationProcess", "Flow", "ApplicationProcess"}:         true,
	{"ApplicationProcess", "Triggering", "ApplicationProcess"}:   true,
	{"TechnologyProcess", "Triggering", "TechnologyProcess"}:     true,
	{"ApplicationComponent", "Assignment", "ApplicationProcess"}: true,
	{"Node", "Composition", "SystemSoftware"}:                    true,
}

var accessTypes = []string{"Read", "Write", "ReadWrite", "Access"}

// validate checks what the exchange format and ArchiMate require of the model,
// and that every view is drawable: known elements, a parent before its children.
func (m *Model) validate() []error {
	var errs []error
	fail := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	ids := map[string]string{} // id -> what it is, across elements, relationships and views
	claim := func(id, what string) {
		if prev, dup := ids[id]; dup {
			fail("%s %q: id already used by %s", what, id, prev)
		}
		ids[id] = what
	}
	types := map[string]string{}
	for _, e := range m.Elements {
		claim(e.ID, "element")
		types[e.ID] = e.Type
		if !slices.Contains(elementTypes, e.Type) {
			fail("element %q: unknown type %q", e.ID, e.Type)
		}
		if e.Name == "" {
			fail("element %q: no name", e.ID)
		}
	}
	rels := map[string]Relationship{}
	for _, r := range m.Relationships {
		claim(r.ID, "relationship")
		rels[r.ID] = r
		st, sok := types[r.Source]
		tt, tok := types[r.Target]
		if !sok || !tok {
			fail("relationship %q: source or target is not an element", r.ID)
			continue
		}
		same := st == tt && (r.Type == "Composition" || r.Type == "Aggregation" || r.Type == "Specialization")
		if r.Type != "Association" && !same && !allowed[[3]string{st, r.Type, tt}] {
			fail("relationship %q: %s %s %s is not in the allowed list", r.ID, st, r.Type, tt)
		}
		if (r.Type == "Access") != (r.Access != "") || (r.Access != "" && !slices.Contains(accessTypes, r.Access)) {
			fail("relationship %q: access must be one of %v exactly when the type is Access", r.ID, accessTypes)
		}
	}
	for _, v := range m.Views {
		claim(v.ID, "view")
		seenM, seenEl := map[string]bool{}, map[string]bool{}
		for _, n := range v.Nodes {
			if _, ok := types[n.El]; !ok {
				fail("view %q, node %q: unknown element %q", v.ID, n.M, n.El)
			}
			if seenM[n.M] || seenEl[n.El] {
				fail("view %q: node %q or element %q appears twice", v.ID, n.M, n.El)
			}
			if n.In != "" && !seenM[n.In] {
				fail("view %q, node %q: parent %q must be listed before it", v.ID, n.M, n.In)
			}
			seenM[n.M], seenEl[n.El] = true, true
		}
		for _, id := range v.Rels {
			r, ok := rels[id]
			if !ok {
				fail("view %q: unknown relationship %q", v.ID, id)
			} else if !seenEl[r.Source] || !seenEl[r.Target] || r.Source == r.Target {
				fail("view %q: relationship %q needs two distinct elements of the view", v.ID, id)
			}
		}
	}
	return errs
}
