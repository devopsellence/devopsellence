# frozen_string_literal: true

module ShellQuoting
  module_function

  def single_quote(value)
    escaped = value.to_s.gsub("'", %q('"'"'))
    "'#{escaped}'"
  end
end
