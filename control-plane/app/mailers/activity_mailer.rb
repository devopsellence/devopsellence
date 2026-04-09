# frozen_string_literal: true

class ActivityMailer < ApplicationMailer
  def hourly_summary(recipient:, users:, deployed_hostnames:, window_start:, window_end:)
    @users = users
    @deployed_hostnames = deployed_hostnames
    @window_start = window_start
    @window_end = window_end

    mail(
      to: Array(recipient),
      subject: subject_line
    )
  end

  private
    def subject_line
      segments = []
      segments << "#{@users.count} #{noun_for(@users.count, singular: "user signup", plural: "user signups")}" if @users.any?
      segments << "#{@deployed_hostnames.count} #{noun_for(@deployed_hostnames.count, singular: "deployed app", plural: "deployed apps")}" if @deployed_hostnames.any?

      "devopsellence hourly activity: #{segments.join(', ')}"
    end

    def noun_for(count, singular:, plural:)
      if count == 1
        singular
      else
        plural
      end
    end
end
