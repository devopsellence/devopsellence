# frozen_string_literal: true

require "zlib"

module Runtime
  module AdvisoryLock
    extend self

    LOCK_NAMESPACE = 20_260_327

    def with_lock(lock_name)
      ApplicationRecord.connection_pool.with_connection do |connection|
        key = nil
        return yield unless postgresql?(connection)

        key = signed_lock_key(lock_name)
        connection.execute("SELECT pg_advisory_lock(#{LOCK_NAMESPACE}, #{key})")
        yield
      ensure
        connection.execute("SELECT pg_advisory_unlock(#{LOCK_NAMESPACE}, #{key})") if connection && key && postgresql?(connection)
      end
    end

    private

    def postgresql?(connection)
      connection.adapter_name.to_s.casecmp("postgresql").zero?
    end

    def signed_lock_key(lock_name)
      value = Zlib.crc32(lock_name.to_s)
      value >= 2**31 ? value - 2**32 : value
    end
  end
end
