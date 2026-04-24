# frozen_string_literal: true

module IngressHostnames
  module_function

  def normalize(value)
    value.to_s.strip.downcase
  end

  def normalize_all(values)
    Array(values).map { |entry| normalize(entry) }.reject(&:blank?).uniq
  end
end
