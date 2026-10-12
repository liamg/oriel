; Code units (text.Unit): @unit.<kind> on the node, @name on its name. Ours, not nvim-treesitter's.
(module (function_definition name: (identifier) @name) @unit.function)
(module (decorated_definition (function_definition name: (identifier) @name)) @unit.function)
(class_definition body: (block (function_definition name: (identifier) @name) @unit.method))
(class_definition body: (block (decorated_definition (function_definition name: (identifier) @name)) @unit.method))
(function_definition body: (block (function_definition name: (identifier) @name) @unit.function))
(class_definition name: (identifier) @name) @unit.type
(lambda) @unit.closure
(if_statement) @unit.if
[(for_statement) (while_statement)] @unit.loop
(match_statement) @unit.switch
(case_clause) @unit.case
[(with_statement) (try_statement)] @unit.block
