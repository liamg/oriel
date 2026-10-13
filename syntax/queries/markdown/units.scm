; Code units (text.Unit): a section per heading, named by the heading's text. Ours, not nvim-treesitter's.
(section (atx_heading (inline) @name)) @unit.section
(section (setext_heading (paragraph) @name)) @unit.section
(fenced_code_block) @unit.block
