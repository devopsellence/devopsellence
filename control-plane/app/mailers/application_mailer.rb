class ApplicationMailer < ActionMailer::Base
  default from: lambda {
    name = Devopsellence::RuntimeConfig.current.mail_from_name
    address = ApplicationMailer.from_address
    name.present? ? %(#{name} <#{address}>) : address
  }
  layout "mailer"

  def self.from_address
    Devopsellence::RuntimeConfig.current.mail_from_address
  end
end
