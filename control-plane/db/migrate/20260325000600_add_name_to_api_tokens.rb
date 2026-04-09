# frozen_string_literal: true

class AddNameToApiTokens < ActiveRecord::Migration[8.1]
  def change
    add_column :api_tokens, :name, :string
  end
end
