# frozen_string_literal: true

module HttpBasicGate
  extend ActiveSupport::Concern

  included do
    before_action :require_http_basic_gate
  end

  private

  def require_http_basic_gate
    return unless http_basic_gate_enabled?

    authenticate_or_request_with_http_basic("devopsellence") do |username, password|
      secure_match?(username, runtime.http_basic_username) &&
        secure_match?(password, runtime.http_basic_password)
    end
  end

  def http_basic_gate_enabled?
    runtime.http_basic_username.present? &&
      runtime.http_basic_password.present?
  end

  def runtime
    Devopsellence::RuntimeConfig.current
  end

  def secure_match?(left, right)
    return false if left.bytesize != right.bytesize

    ActiveSupport::SecurityUtils.secure_compare(left, right)
  end
end
