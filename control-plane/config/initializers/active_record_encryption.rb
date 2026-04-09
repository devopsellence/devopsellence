# frozen_string_literal: true

secret = Rails.application.secret_key_base.to_s
key_generator = ActiveSupport::KeyGenerator.new(secret, iterations: 100_000)

Rails.application.config.active_record.encryption.primary_key = [
  key_generator.generate_key("active_record_encryption.primary_key", 32)
]
Rails.application.config.active_record.encryption.deterministic_key = [
  key_generator.generate_key("active_record_encryption.deterministic_key", 32)
]
Rails.application.config.active_record.encryption.key_derivation_salt =
  key_generator.generate_key("active_record_encryption.key_derivation_salt", 32)
