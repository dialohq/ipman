package main

import (
	"dialo.ai/ipman/pkg/swanparse"
)

// ReconcileVisitor identifies connections and children in conns that are not in sas
type ReconcileVisitor struct {
	Conns           *swanparse.SwanAST
	SasConnections  map[string]bool
	SasChildren     map[string]bool
	MissingConns    map[string]bool
	MissingChildren map[string]bool
}

func NewReconcileVisitor(conns, sas *swanparse.SwanAST) *ReconcileVisitor {
	// Collect all connections and children in sas
	sasCollector := swanparse.NewConnectionCollector()
	sasCollector.VisitAST(sas)

	return &ReconcileVisitor{
		Conns:           conns,
		SasConnections:  sasCollector.Connections,
		SasChildren:     sasCollector.Children,
		MissingConns:    make(map[string]bool),
		MissingChildren: make(map[string]bool),
	}
}

// Override VisitAST to ensure our methods are called
func (r *ReconcileVisitor) VisitAST(ast *swanparse.SwanAST) error {
	for _, entry := range ast.Entry {
		if err := r.VisitEntry(entry); err != nil {
			return err
		}
	}
	return nil
}

// Override VisitEntry to ensure we call our methods
func (r *ReconcileVisitor) VisitEntry(entry *swanparse.Entry) error {
	for _, conn := range entry.Conn {
		if err := r.VisitConn(conn); err != nil {
			return err
		}
	}
	return nil
}

// Override VisitConn to check if connection exists in SAs
func (r *ReconcileVisitor) VisitConn(conn *swanparse.Conn) error {
	// Check if this connection exists in sas
	if !r.SasConnections[conn.Name] {
		r.MissingConns[conn.Name] = true
	}

	// Process child entities in the connection body
	for _, entity := range conn.Body {
		if err := r.VisitEntity(entity); err != nil {
			return err
		}
	}
	return nil
}

// Override VisitEntity to ensure we call our methods
func (r *ReconcileVisitor) VisitEntity(entity *swanparse.Entity) error {
	if entity.Block != nil {
		return r.VisitBlock(entity.Block)
	}
	if entity.Option != nil {
		return r.VisitOption(entity.Option)
	}
	return nil
}

// Override VisitOption with empty implementation
func (r *ReconcileVisitor) VisitOption(option *swanparse.Option) error {
	return nil
}

// Override VisitBlock to check if children exist in SAs
func (r *ReconcileVisitor) VisitBlock(block *swanparse.Block) error {
	// When encountering a children block in the connection AST,
	// check if each child exists in the SAs
	if block.Name == "children" {
		for _, childEntity := range block.Body {
			if childEntity.Block != nil {
				childName := childEntity.Block.Name
				if !r.SasChildren[childName] {
					r.MissingChildren[childName] = true
				}
			}
		}
	}

	// Process all entities in the block
	for _, entity := range block.Body {
		if err := r.VisitEntity(entity); err != nil {
			return err
		}
	}
	return nil
}
