; Code units (text.Unit): @unit.<kind> on the node, @name on its name. Ours, not nvim-treesitter's.
(source_file (function_item name: (identifier) @name) @unit.function)
(mod_item body: (declaration_list (function_item name: (identifier) @name) @unit.function))
(block (function_item name: (identifier) @name) @unit.function)
(impl_item body: (declaration_list (function_item name: (identifier) @name) @unit.method))
(trait_item body: (declaration_list [(function_item name: (identifier) @name) (function_signature_item name: (identifier) @name)] @unit.method))
[(struct_item name: (type_identifier) @name) (enum_item name: (type_identifier) @name) (trait_item name: (type_identifier) @name) (type_item name: (type_identifier) @name) (union_item name: (type_identifier) @name)] @unit.type
(impl_item type: (_) @name) @unit.type
(mod_item name: (identifier) @name) @unit.type
(closure_expression) @unit.closure
(if_expression) @unit.if
[(for_expression) (while_expression) (loop_expression)] @unit.loop
(match_expression) @unit.switch
(match_arm) @unit.case
[(block) (unsafe_block) (async_block)] @unit.block
