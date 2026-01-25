package main

import (
	"dialo.ai/ipman/pkg/swanparse"
)

// StrongSwanVisitor identifies connections and children in conns that are not in sas
type StrongSwanVisitor struct {
	Conns           *swanparse.SwanAST
	SasChildren     map[string]map[string]bool
	MissingConns    map[string]bool
	MissingChildren map[string][]string
}

func NewStrongSwanVisitor(conns, sas *swanparse.SwanAST) *StrongSwanVisitor {
	// Collect all connections and children in sas
	sasCollector := swanparse.NewConnectionCollector()
	sasCollector.VisitAST(sas)

	return &StrongSwanVisitor{
		Conns:           conns,
		SasChildren:     sasCollector.Children,
		MissingConns:    make(map[string]bool),
		MissingChildren: make(map[string][]string),
	}
}

// Override VisitAST to ensure our methods are called
func (r *StrongSwanVisitor) VisitAST(ast *swanparse.SwanAST) error {
	for _, entry := range ast.Entry {
		if err := r.VisitEntry(entry); err != nil {
			return err
		}
	}
	return nil
}

// Override VisitEntry to ensure we call our methods
func (r *StrongSwanVisitor) VisitEntry(entry *swanparse.Entry) error {
	for _, conn := range entry.Conn {
		if err := r.VisitConn(conn); err != nil {
			return err
		}
	}
	return nil
}

// Override VisitConn to check if connection exists in SAs
func (r *StrongSwanVisitor) VisitConn(conn *swanparse.Conn) error {
	// Check if this connection exists in sas
	if _, ok := r.SasChildren[conn.Name]; !ok {
		r.MissingConns[conn.Name] = true
	}

	// Process child entities in the connection body
	for _, entity := range conn.Body {
		if err := r.VisitEntity(entity, conn); err != nil {
			return err
		}
	}
	return nil
}

// Override VisitEntity to ensure we call our methods
func (r *StrongSwanVisitor) VisitEntity(entity *swanparse.Entity, conn *swanparse.Conn) error {
	if entity.Block != nil {
		return r.VisitBlock(entity.Block, conn)
	}
	if entity.Option != nil {
		return r.VisitOption(entity.Option, conn)
	}
	return nil
}

// Override VisitOption with empty implementation
func (r *StrongSwanVisitor) VisitOption(option *swanparse.Option, conn *swanparse.Conn) error {
	return nil
}

// Override VisitBlock to check if children exist in SAs
func (r *StrongSwanVisitor) VisitBlock(block *swanparse.Block, conn *swanparse.Conn) error {
	// When encountering a children block in the connection AST,
	// check if each child exists in the SAs
	if block.Name == "children" {
		for _, childEntity := range block.Body {
			if childEntity.Block != nil {
				childName := childEntity.Block.Name
				exists := false
				if children, ok := r.SasChildren[conn.Name]; ok {
					if children[childName] {
						exists = true
					}
				}
				if !exists {
					r.MissingChildren[conn.Name] = append(r.MissingChildren[conn.Name], childName)
				}
			}
		}
	}

	// Process all entities in the block
	for _, entity := range block.Body {
		if err := r.VisitEntity(entity, conn); err != nil {
			return err
		}
	}
	return nil
}
