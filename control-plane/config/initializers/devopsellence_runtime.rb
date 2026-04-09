# frozen_string_literal: true

require Rails.root.join("lib/devopsellence/runtime_config")

Rails.application.config.x.devopsellence_runtime = Devopsellence::RuntimeConfig.load_current!
