; Code units (text.Unit): @unit.<kind> on the node, @name on its name. Ours, not nvim-treesitter's.
(function_declaration name: (identifier) @name) @unit.function
(method_declaration name: (field_identifier) @name) @unit.method
(type_spec name: (type_identifier) @name) @unit.type
(type_alias name: (type_identifier) @name) @unit.type
(func_literal) @unit.closure
(if_statement) @unit.if
(for_statement) @unit.loop
[(expression_switch_statement) (type_switch_statement) (select_statement)] @unit.switch
[(expression_case) (type_case) (communication_case) (default_case)] @unit.case
(block) @unit.block
