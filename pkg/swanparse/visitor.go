package swanparse

// SwanASTVisitor defines the interface for visitors that traverse SwanAST
type SwanASTVisitor interface {
	VisitAST(ast *SwanAST) error
	VisitEntry(entry *Entry) error
	VisitConn(conn *Conn) error
	VisitEntity(entity *Entity) error
	VisitBlock(block *Block) error
	VisitOption(option *Option) error
}

// ConnectionCollector collects all connection names
type ConnectionCollector struct {
	Connections map[string]bool
	Children    map[string]bool
}

func NewConnectionCollector() *ConnectionCollector {
	return &ConnectionCollector{
		Connections: make(map[string]bool),
		Children:    make(map[string]bool),
	}
}

// Override VisitAST to ensure our methods are called
func (c *ConnectionCollector) VisitAST(ast *SwanAST) error {
	for _, entry := range ast.Entry {
		if err := c.VisitEntry(entry); err != nil {
			return err
		}
	}
	return nil
}

// Override VisitEntry to ensure we call our methods
func (c *ConnectionCollector) VisitEntry(entry *Entry) error {
	for _, conn := range entry.Conn {
		if err := c.VisitConn(conn); err != nil {
			return err
		}
	}
	return nil
}

// Override VisitConn to collect connection names
func (c *ConnectionCollector) VisitConn(conn *Conn) error {
	c.Connections[conn.Name] = true

	// Process child entities in the connection body
	for _, entity := range conn.Body {
		if err := c.VisitEntity(entity); err != nil {
			return err
		}
	}
	return nil
}

// Override VisitEntity to ensure we call our methods
func (c *ConnectionCollector) VisitEntity(entity *Entity) error {
	if entity.Block != nil {
		return c.VisitBlock(entity.Block)
	}
	if entity.Option != nil {
		return c.VisitOption(entity.Option)
	}
	return nil
}

// Override VisitOption with empty implementation
func (c *ConnectionCollector) VisitOption(option *Option) error {
	return nil
}

// Override VisitBlock to collect children information
func (c *ConnectionCollector) VisitBlock(block *Block) error {
	// Look for child-sas blocks (in SA listings)
	if block.Name == "child-sas" {
		for _, entity := range block.Body {
			if entity.Block != nil {
				// Find the name option to get the actual child name
				for _, childEntity := range entity.Block.Body {
					if childEntity.Option != nil && childEntity.Option.Key == "name" &&
						childEntity.Option.Value != nil && childEntity.Option.Value.Single != nil {
						c.Children[*childEntity.Option.Value.Single] = true
						break
					}
				}
			}
		}
		// Also look for direct children blocks (in connection listings)
	} else if block.Name == "children" {
		for _, entity := range block.Body {
			if entity.Block != nil {
				c.Children[entity.Block.Name] = true
			}
		}
	}

	// Process all entities in the block
	for _, entity := range block.Body {
		if err := c.VisitEntity(entity); err != nil {
			return err
		}
	}
	return nil
}
