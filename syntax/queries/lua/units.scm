; Code units (text.Unit): @unit.<kind> on the node, @name on its name. Ours, not nvim-treesitter's.
(function_declaration name: [(identifier) (dot_index_expression)] @name) @unit.function
(function_declaration name: (method_index_expression) @name) @unit.method
(function_definition) @unit.closure
(if_statement) @unit.if
[(for_statement) (while_statement) (repeat_statement)] @unit.loop
(do_statement) @unit.block
