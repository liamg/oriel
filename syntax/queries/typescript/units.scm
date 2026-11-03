; Code units (text.Unit): @unit.<kind> on the node, @name on its name. Ours, not nvim-treesitter's.
[(function_declaration name: (identifier) @name) (generator_function_declaration name: (identifier) @name)] @unit.function
(variable_declarator name: (identifier) @name value: [(arrow_function) (function_expression)]) @unit.function
(method_definition name: (_) @name) @unit.method
[(class_declaration name: (type_identifier) @name) (abstract_class_declaration name: (type_identifier) @name)] @unit.type
(interface_declaration name: (type_identifier) @name) @unit.type
(type_alias_declaration name: (type_identifier) @name) @unit.type
(enum_declaration name: (identifier) @name) @unit.type
[(arrow_function) (function_expression)] @unit.closure
(if_statement) @unit.if
[(for_statement) (for_in_statement) (while_statement) (do_statement)] @unit.loop
(switch_statement) @unit.switch
[(switch_case) (switch_default)] @unit.case
(statement_block) @unit.block
