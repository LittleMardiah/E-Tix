FROM rabbitmq:3.12-management-alpine
ADD https://github.com/rabbitmq/rabbitmq-delayed-message-exchange/releases/download/v3.12.0/rabbitmq_delayed_message_exchange-3.12.0.ez /opt/rabbitmq/plugins/
# ADD https:// hard-codes mode 0600 (root:root); rabbitmq user needs read access.
RUN chmod 644 /opt/rabbitmq/plugins/rabbitmq_delayed_message_exchange-3.12.0.ez
RUN rabbitmq-plugins enable --offline rabbitmq_delayed_message_exchange
